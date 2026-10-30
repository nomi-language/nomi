package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Map update by spread: `%{..base, key => value}`, which means
// `Map.merge(base, %{key => value})`. The first element after the spread
// decides between it and the struct spread: `=>` makes it a map.

func TestParseMapSpread(t *testing.T) {
	cases := []struct {
		src     string
		spread  string // %T of the head
		entries int
	}{
		{`%{..m, "b" => 2}`, "*ast.Ident", 1},
		{`%{..m, "b" => 2, "c" => 3}`, "*ast.Ident", 2},
		{`%{..f(y), "b" => 2}`, "*ast.Call", 1},
		{`%{..a.b, "c" => 2}`, "*ast.FieldAccess", 1},
		{`%{..m, "b" => 2,}`, "*ast.Ident", 1},
		{"%{\n  ..m,\n  \"b\" => 2\n}", "*ast.Ident", 1},
		{"%{\n  ..m,\n  // note\n  \"b\" => 2,\n}", "*ast.Ident", 1},
		// A key that is not a literal.
		{`%{..m, k => v}`, "*ast.Ident", 1},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			expr := parseExpr(t, tc.src)
			lit, ok := expr.(*ast.MapLit)
			if !ok {
				t.Fatalf("expected *ast.MapLit for %q, got %T", tc.src, expr)
			}
			if len(lit.Spreads) != 1 || lit.Spreads[0].At != 0 {
				t.Fatalf("expected one spread first, got %+v", lit.Spreads)
			}
			if got := typeString(lit.Spreads[0].Head); got != tc.spread {
				t.Errorf("spread head type = %s, want %s", got, tc.spread)
			}
			if len(lit.Entries) != tc.entries {
				t.Errorf("entries = %d, want %d", len(lit.Entries), tc.entries)
			}
			if lit.TypeName != nil {
				t.Errorf("a spread literal is always anonymous, got TypeName %v", lit.TypeName)
			}
			if lit.Spreads[0].Line == 0 || lit.Spreads[0].Col == 0 {
				t.Errorf("spread position unset; the diagnostics and hover need it")
			}
		})
	}
}

// `#{..m}` names nothing that says map, so it parses as the struct spread;
// the checker reads the base's type and words its error for a map.
func TestParseMapSpreadAloneIsTheStructForm(t *testing.T) {
	expr := parseExpr(t, "#{..m}")
	if lit, ok := expr.(*ast.StructLit); !ok || lit.Spread == nil {
		t.Fatalf("expected a struct spread for `#{..m}`, got %T", expr)
	}
}
