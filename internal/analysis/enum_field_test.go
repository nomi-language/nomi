package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestEnumStructVariantFieldAccess(t *testing.T) {
	src := `
enum Error {
    HttpError{status: Int, message: String}
    Timeout
}

fn demo(): Int {
    err = Error.HttpError{status: 404, message: "not found"}
    err.status
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildFile(nodes)
	typeErrs := analysis.BuildTypes(fa, nodes)
	checkErrs := analysis.CheckTypes(fa, nodes)

	if len(typeErrs) > 0 || len(checkErrs) > 0 {
		t.Errorf("unexpected errors: type=%v check=%v", typeErrs, checkErrs)
	}

	statusPos := analysis.Pos{Line: 9, Col: 9}
	sym, ok := fa.References[statusPos]
	if !ok {
		t.Fatal("expected reference for err.status field access")
	}
	if sym.Name != "status" || sym.Kind != analysis.SymbolField {
		t.Errorf("expected status field, got %s (kind=%d)", sym.Name, sym.Kind)
	}
}

func TestEmbeddedEnumVariantFieldAccess(t *testing.T) {
	src := `
struct Circle2 {
    radius: Float
}

enum Drawable {
    embeds Circle2
    Line
}

fn demo(): Float {
    c = Drawable.Circle2{radius: 5.0}
    c.radius
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildFile(nodes)
	typeErrs := analysis.BuildTypes(fa, nodes)
	checkErrs := analysis.CheckTypes(fa, nodes)

	if len(typeErrs) > 0 || len(checkErrs) > 0 {
		t.Errorf("unexpected errors: type=%v check=%v", typeErrs, checkErrs)
	}

	// "radius" in "c.radius"
	radiusPos := analysis.Pos{Line: 13, Col: 7}
	sym, ok := fa.References[radiusPos]
	if !ok {
		t.Logf("References:")
		for pos, s := range fa.References {
			t.Logf("  %d:%d -> %s (kind=%d)", pos.Line, pos.Col, s.Name, s.Kind)
		}
		t.Fatal("expected reference for c.radius field access")
	}
	if sym.Name != "radius" || sym.Kind != analysis.SymbolField {
		t.Errorf("expected radius field, got %s (kind=%d)", sym.Name, sym.Kind)
	}
}
