package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// break/continue/return are valid as case-arm bodies (they unwind to the
// enclosing loop/lambda just as they do in statement position).

func TestCaseArm_Break(t *testing.T) {
	expr := parseExpr(t, "case x { _ -> break 1 }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	br, ok := c.Branches[0].Body.(*ast.Break)
	if !ok {
		t.Fatalf("expected branch body *ast.Break, got %T", c.Branches[0].Body)
	}
	lit, ok := br.Value.(*ast.IntLit)
	if !ok || lit.Value != 1 {
		t.Fatalf("expected break value IntLit{1}, got %T %v", br.Value, br.Value)
	}
}

func TestCaseArm_Continue(t *testing.T) {
	expr := parseExpr(t, "case x { _ -> continue }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if _, ok := c.Branches[0].Body.(*ast.Continue); !ok {
		t.Fatalf("expected branch body *ast.Continue, got %T", c.Branches[0].Body)
	}
}

func TestCaseArm_Return(t *testing.T) {
	expr := parseExpr(t, "case x { _ -> return 1 }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	ret, ok := c.Branches[0].Body.(*ast.Return)
	if !ok {
		t.Fatalf("expected branch body *ast.Return, got %T", c.Branches[0].Body)
	}
	lit, ok := ret.Value.(*ast.IntLit)
	if !ok || lit.Value != 1 {
		t.Fatalf("expected return value IntLit{1}, got %T %v", ret.Value, ret.Value)
	}
}

// Bare break (no value) as a case-arm body.
func TestCaseArm_BareBreak(t *testing.T) {
	expr := parseExpr(t, "case x { _ -> break }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	br, ok := c.Branches[0].Body.(*ast.Break)
	if !ok {
		t.Fatalf("expected branch body *ast.Break, got %T", c.Branches[0].Body)
	}
	if br.Value != nil {
		t.Fatalf("expected bare break (nil value), got %v", br.Value)
	}
}
