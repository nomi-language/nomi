package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// Negative numeric literals must parse as patterns in every position the
// grammar allows them — not just inside struct fields. Regression coverage
// for the Go-parser/grammar divergence where the top-level case-arm parser and
// parseSinglePattern (tuple / list / map-value element patterns) rejected a
// leading MINUS with "unexpected token MINUS in (case) pattern".

func TestCaseTopLevelNegativeInt(t *testing.T) {
	expr := parseExpr(t, "case n {\n -2 -> \"neg\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	il, ok := c.Branches[0].Pattern.(*ast.IntLit)
	if !ok || il.Value != -2 {
		t.Fatalf("branch[0]: expected IntLit{-2}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
}

func TestCaseTopLevelNegativeFloat(t *testing.T) {
	expr := parseExpr(t, "case f {\n -2.5 -> \"neg\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	fl, ok := c.Branches[0].Pattern.(*ast.FloatLit)
	if !ok || fl.Value != -2.5 {
		t.Fatalf("branch[0]: expected FloatLit{-2.5}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
}

func TestCaseTopLevelNegativeDecimal(t *testing.T) {
	expr := parseExpr(t, "case d {\n -2.5d -> \"neg\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	dl, ok := c.Branches[0].Pattern.(*ast.DecimalLit)
	if !ok || dl.Lexeme != "-2.5d" {
		t.Fatalf("branch[0]: expected DecimalLit{-2.5d}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
}

func TestCaseTupleNegative(t *testing.T) {
	expr := parseExpr(t, "case t {\n (-2, 3) -> \"found\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	tp, ok := c.Branches[0].Pattern.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("branch[0]: expected TuplePattern, got %T", c.Branches[0].Pattern)
	}
	il, ok := tp.Patterns[0].(*ast.IntLit)
	if !ok || il.Value != -2 {
		t.Fatalf("tuple elem[0]: expected IntLit{-2}, got %T %v", tp.Patterns[0], tp.Patterns[0])
	}
}

func TestCaseListNegative(t *testing.T) {
	expr := parseExpr(t, "case xs {\n [-2, 3] -> \"found\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	lp, ok := c.Branches[0].Pattern.(*ast.ListPattern)
	if !ok {
		t.Fatalf("branch[0]: expected ListPattern, got %T", c.Branches[0].Pattern)
	}
	il, ok := lp.Heads[0].(*ast.IntLit)
	if !ok || il.Value != -2 {
		t.Fatalf("list head[0]: expected IntLit{-2}, got %T %v", lp.Heads[0], lp.Heads[0])
	}
}

func TestCaseMapValueNegative(t *testing.T) {
	expr := parseExpr(t, "case m {\n {\"k\" => -2} -> \"found\"\n _ -> \"other\"\n}")
	c := expr.(*ast.Case)
	mp, ok := c.Branches[0].Pattern.(*ast.MapPattern)
	if !ok {
		t.Fatalf("branch[0]: expected MapPattern, got %T", c.Branches[0].Pattern)
	}
	il, ok := mp.Entries[0].Pattern.(*ast.IntLit)
	if !ok || il.Value != -2 {
		t.Fatalf("map value[0]: expected IntLit{-2}, got %T %v", mp.Entries[0].Pattern, mp.Entries[0].Pattern)
	}
}
