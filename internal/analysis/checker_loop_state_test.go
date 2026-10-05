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

// A stateless `Iter.loop(|| { break 42 })` infers its state type S from the
// break value (Int), so the result is usable as Int — no annotation, no
// Unit default.
func TestLoopState_StatelessBreakValueInferred(t *testing.T) {
	src := `fn use_loop(): Int {
  x = Iter.loop(|| { break 42 })
  x + 1
}`
	expectClean(t, checkWithStdlib(src))
}

// A stateless loop whose break value is a type parameter (`break x` where
// x: T) infers S = T — the result is the function's T, not Unit. (Guards
// the checkBreak nil-subs binding for inference vars.)
func TestLoopState_GenericBreakValueInferred(t *testing.T) {
	src := `fn id_loop<T>(x: T): T {
  Iter.loop(|| { break x })
}
fn main(): Unit {}`
	expectClean(t, checkWithStdlib(src))
}

// Two breaks with incompatible types in one stateless loop over-constrain
// S and are rejected.
func TestLoopState_ConflictingBreakTypesRejected(t *testing.T) {
	src := `fn pick(): Bool {
  True
}
fn use_loop(): Int {
  Iter.loop(|| {
    if pick() { break 1 }
    break "two"
  })
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch")
}

// A stateless `Iter.loop(|| break)` (bare break, no value) still infers Unit.
func TestLoopState_BareBreakStillUnit(t *testing.T) {
	src := `fn main(): Unit {
  Iter.loop(|| { break })
}`
	expectClean(t, checkWithStdlib(src))
}

// A stated loop is unchanged — S comes from the default, break matches it.
func TestLoopState_StatedLoopUnchanged(t *testing.T) {
	src := `fn use_loop(): Int {
  Iter.loop(|n = 0|
    if n >= 3 { break n } else { n + 1 })
}`
	expectClean(t, checkWithStdlib(src))
}

// `loop` is not prelude-exported — a bare `loop(...)` call with no import
// is an unknown name, like any other un-imported stdlib function.
func TestLoop_BareNameWithoutImportRejected(t *testing.T) {
	src := `fn main(): Unit {
  loop(|| { break })
}`
	expectErrorContaining(t, checkWithStdlib(src), "undefined variable 'loop'")
}

// `import std/iter.Iter.{loop}` restores the bare spelling — and the
// iter-sensitivity pass must recognise the selective-import call as an
// iter-callback position, so `break` inside the callback stays legal.
func TestLoop_SelectiveImportBareSpellingWithBreak(t *testing.T) {
	src := `import std/iter.Iter.{loop}
fn main(): Unit {
  n = loop(|n = 0| if n > 2 { break n } else { n + 1 })
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
fn main(): Unit {
  xs = [1, 2, 3]
  ys = It.map(xs, |x| if x == 2 { break } else { x })
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
	src := `fn use_loop(): Int {
  Iter.loop(|n = 0|
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
	expectClean(t, check(`fn f(m: Map<String, Int>): Map<String, Int> {
  Map.map_values(m, |v| if v > 9 { break } else { v + 1 })
}`))
	expectErrorContaining(t, check(`fn apply(f: (Int) -> Int): Int { f(1) }
fn f(): Int {
  apply(|v| if v > 9 { break } else { v + 1 })
}`), "break")
}
