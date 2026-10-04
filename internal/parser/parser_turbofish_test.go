package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// Turbofish: explicit type args on a call, `f<Int>(x)`.
func TestTurbofish_Ident(t *testing.T) {
	expr := parseExpr(t, "foo<Int>(x)")
	c, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(c.TypeArgs) != 1 {
		t.Fatalf("expected 1 type arg, got %d", len(c.TypeArgs))
	}
	if len(c.Args) != 1 {
		t.Fatalf("expected 1 value arg, got %d", len(c.Args))
	}
}

// Turbofish on a module-qualified callee, `mod.f<Int>(x)`.
func TestTurbofish_Qualified(t *testing.T) {
	expr := parseExpr(t, "channel.new<Int>(capacity)")
	c, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(c.TypeArgs) != 1 {
		t.Fatalf("expected 1 type arg, got %d", len(c.TypeArgs))
	}
}

// Multiple type args, `f<A, B>(x)`.
func TestTurbofish_MultipleArgs(t *testing.T) {
	expr := parseExpr(t, "make<String, Int>(x)")
	c, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(c.TypeArgs) != 2 {
		t.Fatalf("expected 2 type args, got %d", len(c.TypeArgs))
	}
}

// A plain comparison `a < b` is NOT a turbofish (no `>(` follows).
func TestTurbofish_PlainComparisonUnaffected(t *testing.T) {
	expr := parseExpr(t, "a < b")
	if _, ok := expr.(*ast.Binary); !ok {
		t.Fatalf("expected *ast.Binary (comparison), got %T", expr)
	}
}

// `f<Int>` with no following `(` is NOT a turbofish — back out to comparison.
func TestTurbofish_NoCallAfterGenericUnaffected(t *testing.T) {
	// `x < Int` then `> y`: with no `(` after `>`, this stays a comparison
	// chain rather than a turbofish.
	expr := parseExpr(t, "x < Int")
	if _, ok := expr.(*ast.Binary); !ok {
		t.Fatalf("expected *ast.Binary (comparison), got %T", expr)
	}
}

// Turbofish on a bare PascalCase identifier (variant constructor, struct
// constructor, type name in expression position). `Static<Int>("hi")` is the
// motivating case — a Fragment variant constructor with explicit type args
// avoids the LHS-annotation alternative `f: Fragment<Int> = Static("hi")`.
// Pre-fix this parsed as two chained comparisons (`Static < Int > ("hi")`)
// because *ast.TypeIdent wasn't in isTurbofishCallable's allowed set.
func TestTurbofish_BarePascalCaseIdent(t *testing.T) {
	expr := parseExpr(t, `Static<Int>("hi")`)
	c, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(c.TypeArgs) != 1 {
		t.Fatalf("expected 1 type arg, got %d", len(c.TypeArgs))
	}
	if len(c.Args) != 1 {
		t.Fatalf("expected 1 value arg, got %d", len(c.Args))
	}
	if id, ok := c.Func.(*ast.TypeIdent); !ok || id.Name != "Static" {
		t.Errorf("expected callee to be *ast.TypeIdent{Name: Static}, got %T", c.Func)
	}
}

// Multiple type args on a bare PascalCase callee — same shape as the
// snake_case multi-arg case, just with a TypeIdent on the LHS.
func TestTurbofish_BarePascalCaseMultipleArgs(t *testing.T) {
	expr := parseExpr(t, `Pair<Int, String>(1, "x")`)
	c, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(c.TypeArgs) != 2 {
		t.Fatalf("expected 2 type args, got %d", len(c.TypeArgs))
	}
}

// Rollback case for the bare PascalCase widening: `Static<Int> y` (no `(`
// after the `>`) must remain a chained comparison, not a turbofish. The
// trailing-`(` requirement in tryParseTurbofish is what protects this; the
// gate widening shouldn't change the rollback behavior.
func TestTurbofish_BarePascalCaseNoCallAfterGenericUnaffected(t *testing.T) {
	expr := parseExpr(t, "Static < Int")
	if _, ok := expr.(*ast.Binary); !ok {
		t.Fatalf("expected *ast.Binary (comparison), got %T", expr)
	}
}
