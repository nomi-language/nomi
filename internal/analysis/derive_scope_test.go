package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// checkSourceSynth is checkSource with the front end's pre-analysis passes
// applied first (LowerDerives + SynthesizeDerives + SynthesizeUniversalDebug),
// so BuildTypes/CheckTypes see the synthesized impl blocks — signatures and
// bodies included — exactly like internal/frontend's Checker.Prepare. checkSource alone hands CheckTypes the pre-synthesis slice, which
// never exercises synthesized code.
func checkSourceSynth(src string) (*FileAnalysis, []TypeError) {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	nodes, synthErrs := SynthesizeDerives(nodes)
	nodes = SynthesizeUniversalDebug(nodes)
	fa := BuildFile(nodes)
	typeErrs := BuildTypes(fa, nodes)
	checkErrs := CheckTypes(fa, nodes)
	all := append([]TypeError{}, synthErrs...)
	all = append(all, fa.TypeErrors...)
	all = append(all, typeErrs...)
	all = append(all, checkErrs...)
	return fa, all
}

// expectNoErrorContaining asserts that no diagnostic contains substr.
func expectNoErrorContaining(t *testing.T, errs []TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			t.Errorf("expected no error containing %q, got: %s", substr, e.Error())
		}
	}
}

// TestDeriveArgScope_NotInScopeErrors pins the derive-arg scope rule: a
// `@derive` arg names its protocol in visible source text, so the protocol
// must be in scope. In a bare build (no stdlib, no prelude) each non-Debug
// arg errors at the arg position with the exact import to add — and that is
// the ONLY diagnostic per arg: the synthesized impl headers, the synthesized
// compare signature's `Ordering` return, and the synthesized bodies' support
// names (Ordering variants, True/False, recursion callees) all resolve
// through the compiler-known route, so no fabricated-position errors pile on
// top of the actionable one.
func TestDeriveArgScope_NotInScopeErrors(t *testing.T) {
	fa, errs := checkSourceSynth(`struct Point {
  x: Int
  y: Int
}
derive Comparable for Point
derive Display for Point
derive Equatable for Point
derive Hashable for Point
`)
	expectError(t, errs, "`Comparable` is not in scope — import `std/comparable.Comparable`")
	expectError(t, errs, "`Display` is not in scope — import `std/display.Display`")
	expectError(t, errs, "`Equatable` is not in scope — import `std/equatable.Equatable`")
	expectError(t, errs, "`Hashable` is not in scope — import `std/hashable.Hashable`")
	// The arg errors carry real source positions (the @derive line), never
	// synth-band ones.
	for _, e := range errs {
		if strings.Contains(e.Message, "is not in scope") && IsSynthesizedLine(e.Line) {
			t.Errorf("derive-arg scope error at synthesized position: %s", e.Error())
		}
	}
	// Synthesized internals are compiler-known — no header / signature /
	// body resolution errors.
	expectNoErrorContaining(t, errs, "undefined interface")
	expectNoErrorContaining(t, errs, "Ordering")
	expectNoErrorContaining(t, errs, "undefined type or variant")
	// Materialization is independent of the arg diagnostics: every (T, Iface)
	// pair is still recorded, so a fixed import is the only step between the
	// user and working derives.
	for _, iface := range []string{"Comparable", "Display", "Equatable", "Hashable", "Debug"} {
		if !fa.Impls["Point"][iface] {
			t.Errorf("Point: (%s) impl not recorded in fa.Impls (got %v)", iface, fa.Impls["Point"])
		}
	}
}

// TestDeriveArgScope_DebugExempt: `@derive Debug` needs no import — Debug is
// compiler-known universally (auto-synthesis, universal conformance, eager
// registration), so naming it as a derive arg carries no scope requirement.
func TestDeriveArgScope_DebugExempt(t *testing.T) {
	_, errs := checkSourceSynth(`struct Point {
  x: Int
}
derive Debug for Point
`)
	expectNoErrors(t, errs)
}

// TestDeriveArgScope_EnumBodySupportNamesCompilerKnown: an enum derive
// produces the richest synthesized bodies — Ordering variants as case
// results, `Ordering.Less` patterns, True/False in equals branches, and
// interface-qualified recursion. With the protocols declared in-file (in
// scope) but Ordering/True/False nowhere, the bodies still check clean:
// every support name resolves through the compiler-known route.
func TestDeriveArgScope_EnumBodySupportNamesCompilerKnown(t *testing.T) {
	fa, errs := checkSourceSynth(`interface Equatable {
  fn equal?(a: self, b: self): Bool
}

interface Hashable {
  fn hash(value: self): Int
}

enum Color {
  Red
  Green
  Blue
}
derive Equatable for Color
derive Hashable for Color
`)
	expectNoErrors(t, errs)
	for _, iface := range []string{"Equatable", "Hashable", "Debug"} {
		if !fa.Impls["Color"][iface] {
			t.Errorf("Color: (%s) impl not recorded in fa.Impls (got %v)", iface, fa.Impls["Color"])
		}
	}
}

// TestHandWrittenNestedImplHeaderStillRequiresScope guards the
// loud-by-construction side of the compiler-known header route: a
// hand-written NESTED impl block — which lowering completes in place while
// keeping its REAL source position — naming an out-of-scope interface still
// errors, proving the synth-band discriminator doesn't leak to hand-written
// code that merely passed through lowering.
func TestHandWrittenNestedImplHeaderStillRequiresScope(t *testing.T) {
	_, errs := checkSourceSynth(`struct P {
  x: Int
}

impl Display for P {
  fn to_string(_value: self): String {
    "p"
  }
}

`)
	expectError(t, errs, "undefined interface 'Display'")
}
