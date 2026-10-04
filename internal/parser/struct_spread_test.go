package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// Struct update by spread — `{..base, field: value}`.
//
// THE MECHANISM IS THE LIST LITERAL'S, MIRRORED. parseListLit detects a bare
// `..` at element position and reads it as a spread, which is unambiguous
// against the infix range operator because the infix form requires a left
// operand that the enclosing parseExpr would already have consumed. The brace
// literal does the same, at the one position its rule allows.
//
// The POSITION RULE IS INVERTED and the diagnostic mirrors it: a list spread
// must be the LAST element (a List is cons cells, so `[0, ..xs]` is the cheap
// direction), a record spread must be the FIRST (base-then-overrides is the
// only order under which "a later field wins" is true).

func structSpreadLit(t *testing.T, src string) *ast.StructLit {
	t.Helper()
	expr := parseExpr(t, src)
	lit, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected *ast.StructLit for %q, got %T", src, expr)
	}
	return lit
}

func TestParseStructSpread(t *testing.T) {
	cases := []struct {
		src        string
		spread     string // %T of the head
		fieldCount int
	}{
		{"{..p, a: 9}", "*ast.Ident", 1},
		{"{..p}", "*ast.Ident", 0},
		{"{..p, a: 9, b: 8}", "*ast.Ident", 2},
		{"{..f(y), a: 9}", "*ast.Call", 1},
		{"{..a.b, c: 2}", "*ast.FieldAccess", 1},
		// Punning after a spread. `{..p, a}` cannot be a block, so the
		// one-field ambiguity that blocks `{a}` does not apply.
		{"{..p, a}", "*ast.Ident", 1},
		// Trailing comma.
		{"{..p, a: 9,}", "*ast.Ident", 1},
		// Stacked, newline-separated.
		{"{\n  ..p\n  a: 9\n}", "*ast.Ident", 1},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			lit := structSpreadLit(t, tc.src)
			if lit.Spread == nil {
				t.Fatalf("expected a spread head, got none")
			}
			if got := typeString(lit.Spread); got != tc.spread {
				t.Errorf("spread head type = %s, want %s", got, tc.spread)
			}
			if len(lit.Fields) != tc.fieldCount {
				t.Errorf("fields = %d, want %d", len(lit.Fields), tc.fieldCount)
			}
			if lit.TypeName != nil {
				t.Errorf("a spread literal is always anonymous, got TypeName %v", lit.TypeName)
			}
			if lit.SpreadLine == 0 {
				t.Errorf("SpreadLine unset; the position diagnostics and hover need it")
			}
		})
	}
}

func TestParseStructSpreadRejectsBadPositions(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"after a field":    {"{a: 1, ..p}", "struct spread `..` must be the first element"},
		"after a pun":      {"{a, ..p}", "struct spread `..` must be the first element"},
		"two spreads":      {"{..a, ..b}", "struct spread `..` must be the first element"},
		"map spread":       {"{..m, \"b\" => 2}", "map literals do not support spread `..`"},
		"named form":       {"Cfg{..p, a: 1}", "struct spread `..` has no named form"},
		"named form alone": {"Cfg{..p}", "struct spread `..` has no named form"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex("x = " + tc.src))
			if err == nil {
				t.Fatalf("expected a parse error for %q", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// The infix range operator is untouched. This is the collision objection the
// design answered by measurement, kept as a test so it stays answered.
func TestParseStructSpreadDoesNotDisturbRanges(t *testing.T) {
	for _, src := range []string{"x = 0..3", "x = 0..=3", "x = [0, ..xs]", "x = [1..5]"} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q no longer parses: %v", src, err)
		}
	}
}

// A brace with no leading `..` is unchanged: block, map literal, anonymous
// struct literal. The spread check runs first, so this is the guard that it
// only claims what starts with `..`.
func TestParseStructSpreadLeavesOtherBracesAlone(t *testing.T) {
	cases := map[string]string{
		"anon struct": "x = {a: 1}",
		"pun":         "x = {a, b}",
		"map":         "x = {\"a\" => 1}",
		"block":       "x = {\n  y = 1\n  y\n}",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			nodes, err := Parse(lexer.Lex(src))
			if err != nil {
				t.Fatalf("%q: %v", src, err)
			}
			b, ok := nodes[0].(*ast.Binding)
			if !ok {
				t.Fatalf("expected a binding, got %T", nodes[0])
			}
			if lit, isLit := b.Value.(*ast.StructLit); isLit && lit.Spread != nil {
				t.Errorf("%q was read as a struct spread", src)
			}
		})
	}
}

func typeString(n ast.Node) string {
	switch n.(type) {
	case *ast.Ident:
		return "*ast.Ident"
	case *ast.Call:
		return "*ast.Call"
	case *ast.FieldAccess:
		return "*ast.FieldAccess"
	}
	return "other"
}
