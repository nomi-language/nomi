package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// checkWithStdlib builds + type-checks a source string with the prelude and
// any explicitly-used stdlib file API objects in scope.
func checkWithStdlib(src string) []analysis.TypeError {
	src = withStdlibTestImports(src)
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
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
	lib := std.Load()
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
	const help = "to end with a value, give the loop a state, as in `Iter.loop(fn n = 0 -> …)` with `break n`; or write `break` alone"
	if len(e.Hints) != 1 || e.Hints[0] != help {
		t.Fatalf("hints: got %q, want [%q]", e.Hints, help)
	}
}

// A stateless loop's state is Unit, so `break` with a value of another type
// is an error at the value.
func TestLoopState_StatelessBreakValueRejected(t *testing.T) {
	expectOneLoopError(t, checkLoopSource(t, `fn main() {
  Iter.loop(fn -> { break 42 })
}`), "a stateless `Iter.loop` answers Unit, so `break` cannot give it Int", 2, 27)
	expectOneLoopError(t, checkLoopSource(t, `import std/io

fn main() {
  Iter.loop(fn -> {
    Ok(Some(line)) = io.read_line() else { break }
    case String.to_int(line) {
      Some(v) -> break v
      None -> continue
    }
  })
}`), "a stateless `Iter.loop` answers Unit, so `break` cannot give it Int", 7, 24)
}

// A stateless loop's callback result is its next state, Unit, so a body
// ending in a value of another type is an error at that value.
func TestLoopState_StatelessTailValueRejected(t *testing.T) {
	expectOneLoopError(t, checkLoopSource(t, `fn pick() -> Bool { True }

fn main() {
  Iter.loop(fn -> {
    if pick() { break }
    1 + 2
  })
}`), "a stateless `Iter.loop` has state Unit, so its callback cannot end in Int", 6, 5)
}

// A bare `break`, `break Unit`, `continue` and a Unit tail are a stateless
// loop's whole vocabulary, and the loop's value is Unit.
func TestLoopState_StatelessLoopIsUnit(t *testing.T) {
	expectClean(t, checkLoopSource(t, `import std/io

fn pick() -> Bool { True }

fn main() {
  u: Unit = Iter.loop(fn -> {
    if pick() { break Unit }
    if pick() { continue }
    if pick() { break }
    io.print("again")
  })
  _ = u
}`))
	// The loop is Unit, not Int, so it does not stand where an Int is wanted.
	errs := checkLoopSource(t, `fn main() {
  n: Int = Iter.loop(fn -> { break })
  _ = n
}`)
	if len(errs) == 0 {
		t.Fatal("the front end admits a stateless loop where an Int is wanted")
	}
	expectErrorContaining(t, errs, "Unit")
}

// A loop that breaks with a value of a type parameter takes that type from a
// state seeded with it.
func TestLoopState_GenericBreakValueFromASeededState(t *testing.T) {
	src := `fn id_loop<T>(x: T) -> T {
  Iter.loop(fn _seed = x -> { break x })
}
fn main() -> Unit {}`
	expectClean(t, checkWithStdlib(src))
}

// Two breaks with incompatible types in one loop are rejected.
func TestLoopState_ConflictingBreakTypesRejected(t *testing.T) {
	src := `fn pick() -> Bool {
  True
}
fn use_loop() -> Int {
  Iter.loop(fn n = 0 -> {
    if pick() { break n }
    break "two"
  })
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch")
}

// A stateless `Iter.loop(fn -> break)` (bare break, no value) still infers Unit.
func TestLoopState_BareBreakStillUnit(t *testing.T) {
	src := `fn main() -> Unit {
  Iter.loop(fn -> { break })
}`
	expectClean(t, checkWithStdlib(src))
}

// A stated loop is unchanged — S comes from the default, break matches it.
func TestLoopState_StatedLoopUnchanged(t *testing.T) {
	src := `fn use_loop() -> Int {
  Iter.loop(fn n = 0 ->
    if n >= 3 { break n } else { n + 1 })
}`
	expectClean(t, checkWithStdlib(src))
}

// `loop` is not prelude-exported — a bare `loop(...)` call with no import
// is an unknown name, like any other un-imported stdlib function.
func TestLoop_BareNameWithoutImportRejected(t *testing.T) {
	src := `fn main() -> Unit {
  loop(fn -> { break })
}`
	expectErrorContaining(t, checkWithStdlib(src), "undefined variable 'loop'")
}

// `import std/iter.Iter.{loop}` restores the bare spelling — and the
// iter-sensitivity pass must recognise the selective-import call as an
// iter-callback position, so `break` inside the callback stays legal.
func TestLoop_SelectiveImportBareSpellingWithBreak(t *testing.T) {
	src := `import std/iter.Iter.{loop}
fn main() -> Unit {
  n = loop(fn n = 0 -> if n > 2 { break n } else { n + 1 })
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	expectClean(t, errs)
}

// Aliased Iter callbacks go through the same symbol-resolution path
// as selective imports: `import std/iter.Iter as It` + `It.map(...)` never
// matches the syntactic "Iter.map" key in iterCallbackSlots, so break/continue
// legality rests on discovering the imported owner scope through the alias.
func TestIterCallback_ImportAliasWithBreak(t *testing.T) {
	src := `import std/iter.Iter as It
fn main() -> Unit {
  xs = [1, 2, 3]
  ys = It.map(xs, fn x -> if x == 2 { break } else { x })
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	expectClean(t, errs)
}

// A stated loop whose break value mismatches the state type is still an
// error: the break value's type must match the state's type (spec §12,
// *`loop`*).
func TestLoopState_StatedLoopBreakMismatchStillErrors(t *testing.T) {
	src := `fn use_loop() -> Int {
  Iter.loop(fn n = 0 ->
    if n >= 3 { break "done" } else { n + 1 })
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch")
}

// The Map owner's callback functions are recognised like Iter's: `break`
// inside Map.map_values's callback is legal, and the same `break` in a
// function that takes no iter callback is not.
func TestIterCallback_MapOwnerCallback(t *testing.T) {
	check := func(src string) []analysis.TypeError {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
		lib := std.Load()
		fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
		analysis.AttachStdlibProjectImpls(fa, lib.Files)
		var errs []analysis.TypeError
		errs = append(errs, analysis.BuildTypes(fa, nodes)...)
		errs = append(errs, analysis.CheckTypes(fa, nodes)...)
		return append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	}
	expectClean(t, check(`fn f(m: Map<String, Int>) -> Map<String, Int> {
  Map.map_values(m, fn v -> if v > 9 { break } else { v + 1 })
}`))
	expectErrorContaining(t, check(`fn apply(f: (Int) -> Int) -> Int { f(1) }
fn f() -> Int {
  apply(fn v -> if v > 9 { break } else { v + 1 })
}`), "break")
}

// A bare `break` in a stated loop stops with the current state, so it has
// the state's type: the loop's value is S whether a bare or a valued break
// ends it.
func TestLoopState_BareBreakInAStatedLoopHasTheStateType(t *testing.T) {
	src := `struct Account {
  balance: Int = 0
}

fn open(inputs: List<Int>) -> Account {
  Iter.loop(fn account = #Account{} -> {
    if Iter.empty?(inputs) { break }
    if account.balance > 3 { break #{..account, balance: 0} }
    #{..account, balance: account.balance + 1}
  })
}

fn count() -> Int {
  Iter.loop(fn n = 0 -> if n > 3 { break } else { n + 1 })
}`
	expectClean(t, checkWithStdlib(src))
}
