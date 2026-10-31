package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
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
	if len(sl.Items) != 1 {
		t.Fatalf("expected 1 element, got %d", len(sl.Items))
	}
	if h, ok := sl.Items[0].(*ast.IntLit); !ok || h.Value != 1 {
		t.Errorf("item[0]: expected IntLit{1}, got %T %v", sl.Items[0], sl.Items[0])
	}
	if len(sl.Spreads) != 1 || sl.Spreads[0].At != 1 {
		t.Fatalf("expected one spread after the element, got %+v", sl.Spreads)
	}
	tail, ok := sl.Spreads[0].Head.(*ast.Ident)
	if !ok || tail.Name != "xs" {
		t.Errorf("spread: expected Ident{xs}, got %T %v", sl.Spreads[0].Head, sl.Spreads[0].Head)
	}
}

// spreadText is a literal's elements and spreads in the order written, a
// spread as `..` and its head's text, an element as its text.
func spreadText(items []ast.Node, spreads []ast.Spread) string {
	var parts []string
	for _, n := range ast.Operands(items, spreads) {
		text := nodeText(n)
		if ast.IsSpreadHead(n, spreads) {
			text = ".." + text
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

func nodeText(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.IntLit:
		return v.Lexeme
	case *ast.StringLit:
		return v.Value
	case *ast.CurrentValue:
		return ""
	}
	return n.NodeType()
}

// TestParseCollectionSpreadsAnywhere: a list, vector, set or anonymous map
// literal takes any number of spreads, anywhere among its elements, each
// recorded with the number of elements before it.
func TestParseCollectionSpreadsAnywhere(t *testing.T) {
	for _, tc := range []struct {
		src, want string
	}{
		{"[..xs, 3]", "..xs 3"},
		{"[1, ..xs, 3]", "1 ..xs 3"},
		{"[..xs, ..ys]", "..xs ..ys"},
		{"[1, ..xs, 2, ..ys, 3]", "1 ..xs 2 ..ys 3"},
		{"[..xs, 3,]", "..xs 3"},
		{"[1, ..xs,]", "1 ..xs"},
		{"#[1, ..v, 2]", "1 ..v 2"},
		{"#[..a, ..b]", "..a ..b"},
		{"#[..a, 1, ..b]", "..a 1 ..b"},
		{"%[3, ..s]", "3 ..s"},
		{"%[..s, ..t]", "..s ..t"},
		{"%[1, ..s, 2, ..t]", "1 ..s 2 ..t"},
		{`%{"c" => 3, ..m}`, "c ..m"},
		{`%{..m, ..n}`, "..m ..n"},
		{`%{..m, "a" => 1, ..n, "b" => 2}`, "..m a ..n b"},
		{"%{\n  \"a\" => 1,\n  ..m,\n  ..n,\n}", "a ..m ..n"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			var got string
			switch v := parseExpr(t, tc.src).(type) {
			case *ast.ListSpreadLit:
				got = spreadText(v.Items, v.Spreads)
			case *ast.VectorLit:
				got = spreadText(v.Items, v.Spreads)
			case *ast.SetLit:
				got = spreadText(v.Items, v.Spreads)
			case *ast.MapLit:
				keys := make([]ast.Node, len(v.Entries))
				for i, e := range v.Entries {
					keys[i] = e.Key
				}
				got = spreadText(keys, v.Spreads)
			default:
				t.Fatalf("parsed as %T", v)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseNamedLiteralsRefuseSpreads: a literal that names its type takes
// no spread anywhere; the spread's head carries the type.
func TestParseNamedLiteralsRefuseSpreads(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`%Kvs{"a" => 1, ..m}`, "map spread `..` has no named form"},
		{`%Kvs{..m, "a" => 1}`, "map spread `..` has no named form"},
		{`%Tags["a", ..s]`, "set spread `..` has no named form"},
		{`#Path[1, ..v]`, "vector spread `..` has no named form"},
	} {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.src, err, tc.want)
		}
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
	pat := parseFirstCasePattern(t, "case xs { [h, ..] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected *ast.ListPattern, got %T", pat)
	}
	if _, ok := lp.TailSpread.(*ast.WildcardPattern); !ok {
		t.Errorf("tail: expected WildcardPattern, got %T", lp.TailSpread)
	}
}

func TestParsePatternListSpreadTakesTrailingElements(t *testing.T) {
	pat := parseFirstCasePattern(t, "case xs { [a, ..rest, b] -> 1 }")
	lp, ok := pat.(*ast.ListPattern)
	if !ok || len(lp.Heads) != 1 || len(lp.Tails) != 1 {
		t.Fatalf("expected one head and one tail, got %#v", pat)
	}
	if rest, ok := lp.TailSpread.(*ast.IdentPattern); !ok || rest.Name != "rest" {
		t.Errorf("rest: expected IdentPattern{rest}, got %T %v", lp.TailSpread, lp.TailSpread)
	}
}
