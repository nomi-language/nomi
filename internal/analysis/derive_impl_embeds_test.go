package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// buildDeriveImplWithStdlib mirrors buildWithStdlibForDerive (derive_bounds_test.go)
// but inserts the LowerDerives pass between parse and build — the interface-
// header `derive Iface` surface is a body conformance line that only becomes
// a derive once LowerDerives stamps the synthetic `@derive Iface` decorator
// onto the owning declaration. Without lowering first, CheckDeriveBounds (keyed
// off type-decl `@derive` decorators) would never see the derive at all.
func buildDeriveImplWithStdlib(src string) *analysis.FileAnalysis {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = analysis.LowerDerives(nodes)
	lib := loadStdlibOnce()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	fa.TypeErrors = append(fa.TypeErrors, analysis.CheckDeriveBounds(fa, nodes)...)
	analysis.BuildTypes(fa, nodes)
	fa.TypeErrors = append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)
	fa.TypeErrors = append(fa.TypeErrors, analysis.FinalizeCoherence(fa)...)
	return fa
}

// TestDeriveImplOnEmbedsEnumRecordsTransitiveDemand is the regression pin for
// the derived embedded-variant bug: a `derive Iface` line on an enum
// with `embeds T` must record the transitive `(Iface, T)` manifest
// demand its synthesized body dispatches through — exactly as a `@derive Iface`
// decorator does. The canonical real-world failure was `std/bool.nomi`'s `Bool`
// enum (`embeds True`/`False`): it type-checked clean but trapped at
// runtime with `Display.to_string: no implementation for type 'True'` because
// the lowering never fed the type through CheckDeriveBounds's demand-recording.
//
// The discriminating probe: the embedded payload type (`NoImpl`) has NO
// Equatable impl. If the transitive demand is recorded (the fix), the manifest
// demand drives DetectMissingImpls to a precise "no impl of `Equatable` for
// `NoImpl`" diagnostic. Before the fix the demand was never recorded, so the
// build was silent here and the gap only surfaced as a runtime dispatch miss.
func TestDeriveImplOnEmbedsEnumRecordsTransitiveDemand(t *testing.T) {
	src := `struct NoImpl { x: Int }

enum E {
  embeds NoImpl

}
derive Equatable for E`

	fa := buildDeriveImplWithStdlib(src)
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Equatable` for `NoImpl`") {
		t.Fatalf("expected the embedded-payload missing-impl error — derive lowering must record the transitive (Equatable, NoImpl) demand exactly like @derive; got:\n  %s", errMsgs(fa.TypeErrors))
	}
}

// TestDeriveImplOnEmbedsEnumWithImplSatisfied is the companion happy-path pin:
// when the embedded payload DOES implement the derived interface, no missing-
// impl error fires and the transitive demand is satisfied. Guards against the
// fix over-reporting (e.g. recording a demand it then can't resolve).
func TestDeriveImplOnEmbedsEnumWithImplSatisfied(t *testing.T) {
	src := `struct HasImpl {
  x: Int

}
derive Equatable for HasImpl

enum E {
  embeds HasImpl

}
derive Equatable for E`

	fa := buildDeriveImplWithStdlib(src)
	if hasErrSubstring(fa.TypeErrors, "no impl of") {
		t.Fatalf("embedded payload impl Equatable — no missing-impl error expected; got:\n  %s", errMsgs(fa.TypeErrors))
	}
}
