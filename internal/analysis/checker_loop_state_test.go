package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"strings"
	"testing"
)

// checkWithStdlib builds + type-checks a source string with the prelude and
// any explicitly-used stdlib file API objects in scope.
func checkWithStdlib(src string) []analysis.TypeError {
	src = withStdlibTestImports(src)
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	return errs
}

func expectErrorContaining(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

// checkLoopSource checks src as written, with no imports added, so a
// diagnostic's line and column are the source's own. A parse error fails the
// test: the checker would see only what the parser recovered.
func checkLoopSource(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("the source does not parse: %v", perrs)
	}
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	return append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
}

// expectOneLoopError fails unless errs is exactly one error with message
// msg at line:col carrying the stateless-loop help.
func expectOneLoopError(t *testing.T, errs []analysis.TypeError, msg string, line, col int) {
	t.Helper()
	if len(errs) != 1 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("the front end admits this, or reports more than the one error; got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
	e := errs[0]
	if e.Message != msg || e.Line != line || e.Col != col {
		t.Fatalf("got %q at %d:%d, want %q at %d:%d", e.Message, e.Line, e.Col, msg, line, col)
	}
	const help = "to end with a value, give the loop a state, as in `Iter.loop(0, fn n -> …)` with `break n`; or write `break` alone"
	if len(e.Hints) != 1 || e.Hints[0] != help {
		t.Fatalf("hints: got %q, want [%q]", e.Hints, help)
	}
}

// A stateless loop's state is Unit, so `break` with a value of another type
// is an error at the value.
func TestLoopState_StatelessBreakValueRejected(t *testing.T) {
	expectOneLoopError(t, checkLoopSource(t, `fn main()
  Iter.loop(fn ->
      break 42
  end)
end`), "a stateless `Iter.loop` returns Unit, so `break` cannot give it Int", 3, 13)
	expectOneLoopError(t, checkLoopSource(t, `import std::io

fn main()
  Iter.loop(fn ->
    Ok(Some(line)) = io.read_line() else
        break
    end
    case String.to_int(line)
      Some(v) -> break v
      None -> continue
    end
  end)
end`), "a stateless `Iter.loop` returns Unit, so `break` cannot give it Int", 9, 24)
}

// A stateless loop's callback result is its next state, Unit, so a body
// ending in a value of another type is an error at that value.
func TestLoopState_StatelessTailValueRejected(t *testing.T) {
	expectOneLoopError(t, checkLoopSource(t, `fn pick() -> Bool
    True
end

fn main()
  Iter.loop(fn ->
    if pick() then break
    1 + 2
  end)
end`), "a stateless `Iter.loop` has state Unit, so its callback cannot end in Int", 8, 5)
}

// A bare `break`, `break Unit`, `continue` and a Unit tail are a stateless
// loop's whole vocabulary, and the loop's value is Unit.
func TestLoopState_StatelessLoopIsUnit(t *testing.T) {
	expectClean(t, checkLoopSource(t, `import std::io

fn pick() -> Bool
    True
end

fn main()
  u: Unit = Iter.loop(fn ->
    if pick() then break Unit
    if pick() then continue
    if pick() then break
    io.print("again")
  end)
  _ = u
end`))
	// The loop is Unit, not Int, so it does not stand where an Int is wanted.
	errs := checkLoopSource(t, `fn main()
  n: Int = Iter.loop(fn ->
      break
  end)
  _ = n
end`)
	if len(errs) == 0 {
		t.Fatal("the front end admits a stateless loop where an Int is wanted")
	}
	expectErrorContaining(t, errs, "Unit")
}

// A loop that breaks with a value of a type parameter takes that type from a
// state seeded with it.
func TestLoopState_GenericBreakValueFromASeededState(t *testing.T) {
	src := `fn id_loop<T>(x: T) -> T
  Iter.loop(x, fn _seed ->
      break x
  end)
end
fn main() -> Unit end`
	expectClean(t, checkWithStdlib(src))
}

// Two breaks with incompatible types in one loop are rejected.
func TestLoopState_ConflictingBreakTypesRejected(t *testing.T) {
	src := `fn pick() -> Bool
  True
end
fn use_loop() -> Int
  Iter.loop(0, fn n ->
    if pick() then break n
    break "two"
  end)
end`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch")
}

// A stateless `Iter.loop(fn -> break)` (bare break, no value) still infers Unit.
func TestLoopState_BareBreakStillUnit(t *testing.T) {
	src := `fn main() -> Unit
  Iter.loop(fn ->
      break
  end)
end`
	expectClean(t, checkWithStdlib(src))
}

// A stated loop is unchanged — S comes from the default, break matches it.
func TestLoopState_StatedLoopUnchanged(t *testing.T) {
	src := `fn use_loop() -> Int
  Iter.loop(0, fn n ->
    if n >= 3 then break n else n + 1
  end)
end`
	expectClean(t, checkWithStdlib(src))
}

// `loop` is not prelude-exported — a bare `loop(...)` call with no import
// is an unknown name, like any other un-imported stdlib function.
func TestLoop_BareNameWithoutImportRejected(t *testing.T) {
	src := `fn main() -> Unit
  loop(fn ->
      break
  end)
end`
	expectErrorContaining(t, checkWithStdlib(src), "undefined variable 'loop'")
}

// `import std::iter.Iter.{loop}` restores the bare spelling — and the
// iter-sensitivity pass must recognise the selective-import call as an
// iter-callback position, so `break` inside the callback stays legal.
func TestLoop_SelectiveImportBareSpellingWithBreak(t *testing.T) {
	src := `import std::iter.Iter.{loop}
fn main() -> Unit
  n = loop(0, fn n -> if n > 2 then break n else n + 1)
end`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	expectClean(t, errs)
}

// Aliased Iter callbacks go through the same symbol-resolution path
// as selective imports: `import std::iter.Iter as It` + `It.map(...)` never
// matches the syntactic "Iter.map" key in iterCallbackSlots, so break/continue
// legality rests on discovering the imported owner scope through the alias.
func TestIterCallback_ImportAliasWithBreak(t *testing.T) {
	src := `import std::iter.Iter as It
fn main() -> Unit
  xs = [1, 2, 3]
  ys = It.map(xs, fn x -> if x == 2 then break else x)
end`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	expectClean(t, errs)
}

// A stated loop whose break value mismatches the state type is still an
// error: the break value's type must match the state's type (spec §13,
// *`loop`*).
func TestLoopState_StatedLoopBreakMismatchStillErrors(t *testing.T) {
	src := `fn use_loop() -> Int
  Iter.loop(0, fn n ->
    if n >= 3 then break "done" else n + 1
  end)
end`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch")
}

// The Map owner's callback functions are recognised like Iter's: `break`
// inside Map.map_values's callback is legal, and the same `break` in a
// function that takes no iter callback is not.
func TestIterCallback_MapOwnerCallback(t *testing.T) {
	check := func(src string) []analysis.TypeError {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
		lib := loadStd()
		fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
		analysis.AttachStdlibProjectImpls(fa, lib.Files)
		var errs []analysis.TypeError
		errs = append(errs, analysis.BuildTypes(fa, nodes)...)
		errs = append(errs, analysis.CheckTypes(fa, nodes)...)
		return append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	}
	expectClean(t, check(`fn f(m: Map<String, Int>) -> Map<String, Int>
  Map.map_values(m, fn v -> if v > 9 then break else v + 1)
end`))
	expectErrorContaining(t, check(`fn apply(f: (Int) -> Int) -> Int
    f(1)
end
fn f() -> Int
  apply(fn v -> if v > 9 then break else v + 1)
end`), "break")
}

// A bare `break` in a stated loop stops with the current state, so it has
// the state's type: the loop's value is S whether a bare or a valued break
// ends it.
func TestLoopState_BareBreakInAStatedLoopHasTheStateType(t *testing.T) {
	src := `struct Account
  balance: Int = 0
end

fn open(inputs: List<Int>) -> Account
  Iter.loop(Account{}, fn account ->
    if Iter.is_empty(inputs) then break
    if account.balance > 3 then break {..account, balance: 0}
    {..account, balance: account.balance + 1}
  end)
end

fn count() -> Int
  Iter.loop(0, fn n -> if n > 3 then break else n + 1)
end`
	expectClean(t, checkWithStdlib(src))
}
