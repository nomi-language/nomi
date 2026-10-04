package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Expression-side [h, ..t] spread literals
// ---------------------------------------------------------------------------

func TestParseListSpreadSingleHead(t *testing.T) {
	expr := parseExpr(t, "[1, ..xs]")
	sl, ok := expr.(*ast.ListSpreadLit)
	if !ok {
		t.Fatalf("expected *ast.ListSpreadLit, got %T", expr)
	}
	if len(sl.Heads) != 1 {
		t.Fatalf("expected 1 head, got %d", len(sl.Heads))
	}
	if h, ok := sl.Heads[0].(*ast.IntLit); !ok || h.Value != 1 {
		t.Errorf("head[0]: expected IntLit{1}, got %T %v", sl.Heads[0], sl.Heads[0])
	}
	tail, ok := sl.TailSpread.(*ast.Ident)
	if !ok || tail.Name != "xs" {
		t.Errorf("tail: expected Ident{xs}, got %T %v", sl.TailSpread, sl.TailSpread)
	}
}

func TestParseListSpreadMultipleHeads(t *testing.T) {
	expr := parseExpr(t, "[a, b, c, ..rest]")
	sl, ok := expr.(*ast.ListSpreadLit)
	if !ok {
		t.Fatalf("expected *ast.ListSpreadLit, got %T", expr)
	}
	if len(sl.Heads) != 3 {
		t.Fatalf("expected 3 heads, got %d", len(sl.Heads))
	}
	tail, ok := sl.TailSpread.(*ast.Ident)
	if !ok || tail.Name != "rest" {
		t.Errorf("tail: expected Ident{rest}, got %T %v", sl.TailSpread, sl.TailSpread)
	}
}

func TestParseListSpreadNoHeads(t *testing.T) {
	expr := parseExpr(t, "[..xs]")
	sl, ok := expr.(*ast.ListSpreadLit)
	if !ok {
		t.Fatalf("expected *ast.ListSpreadLit, got %T", expr)
	}
	if len(sl.Heads) != 0 {
		t.Fatalf("expected 0 heads, got %d", len(sl.Heads))
	}
	tail, ok := sl.TailSpread.(*ast.Ident)
	if !ok || tail.Name != "xs" {
		t.Errorf("tail: expected Ident{xs}, got %T %v", sl.TailSpread, sl.TailSpread)
	}
}

func TestParseListSpreadRejectsTrailingElement(t *testing.T) {
	src := "[1, ..xs, 2]"
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for %q, got nil", src)
	}
	if !strings.Contains(err.Error(), "spread") || !strings.Contains(err.Error(), "last") {
		t.Errorf("expected error mentioning spread/last, got: %v", err)
	}
}

func TestParseListSpreadRejectsMultipleTails(t *testing.T) {
	src := "[1, ..xs, ..ys]"
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for %q, got nil", src)
	}
}

func TestParseListWithBoundedRangeElement(t *testing.T) {
	// `[1..5]` must still parse as a list literal containing one
	// bounded-range expression — the LHS-bearing `..` is the infix range
	// operator, not a spread.
	expr := parseExpr(t, "[1..5]")
	ll, ok := expr.(*ast.ListLit)
	if !ok {
		t.Fatalf("expected *ast.ListLit, got %T", expr)
	}
	if len(ll.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(ll.Items))
	}
	if _, ok := ll.Items[0].(*ast.RangeLit); !ok {
		t.Errorf("expected RangeLit item, got %T", ll.Items[0])
	}
}

// ---------------------------------------------------------------------------
// Pattern-side [h, ..t] spread patterns (exercised via case)
// ---------------------------------------------------------------------------

func parseFirstCasePattern(t *testing.T, src string) ast.Node {
	t.Helper()
	expr := parseExpr(t, src)
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) == 0 {
		t.Fatalf("expected at least one branch")
	}
	return c.Branches[0].Pattern
}

func TestParsePatternListSpreadSingleHead(t *testing.T) {
	pat := parseFirstCasePattern(t, "case xs { [h, ..t] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected *ast.ListPattern, got %T", pat)
	}
	if len(lp.Heads) != 1 {
		t.Fatalf("expected 1 head, got %d", len(lp.Heads))
	}
	if h, ok := lp.Heads[0].(*ast.IdentPattern); !ok || h.Name != "h" {
		t.Errorf("head[0]: expected IdentPattern{h}, got %T %v", lp.Heads[0], lp.Heads[0])
	}
	tail, ok := lp.TailSpread.(*ast.IdentPattern)
	if !ok || tail.Name != "t" {
		t.Errorf("tail: expected IdentPattern{t}, got %T %v", lp.TailSpread, lp.TailSpread)
	}
}

func TestParsePatternListSpreadMultipleHeads(t *testing.T) {
	pat := parseFirstCasePattern(t, "case xs { [a, b, ..rest] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected *ast.ListPattern, got %T", pat)
	}
	if len(lp.Heads) != 2 {
		t.Fatalf("expected 2 heads, got %d", len(lp.Heads))
	}
	tail, ok := lp.TailSpread.(*ast.IdentPattern)
	if !ok || tail.Name != "rest" {
		t.Errorf("tail: expected IdentPattern{rest}, got %T %v", lp.TailSpread, lp.TailSpread)
	}
}

func TestParsePatternListSpreadNoHeads(t *testing.T) {
	pat := parseFirstCasePattern(t, "case xs { [..t] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected *ast.ListPattern, got %T", pat)
	}
	if len(lp.Heads) != 0 {
		t.Fatalf("expected 0 heads, got %d", len(lp.Heads))
	}
	tail, ok := lp.TailSpread.(*ast.IdentPattern)
	if !ok || tail.Name != "t" {
		t.Errorf("tail: expected IdentPattern{t}, got %T %v", lp.TailSpread, lp.TailSpread)
	}
}

func TestParsePatternListSpreadWildcardTail(t *testing.T) {
	pat := parseFirstCasePattern(t, "case xs { [h, .._] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected *ast.ListPattern, got %T", pat)
	}
	if _, ok := lp.TailSpread.(*ast.WildcardPattern); !ok {
		t.Errorf("tail: expected WildcardPattern, got %T", lp.TailSpread)
	}
}

func TestParsePatternListSpreadRejectsTrailingElement(t *testing.T) {
	src := "case xs { [a, ..rest, b] -> 1 }"
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for %q, got nil", src)
	}
}
