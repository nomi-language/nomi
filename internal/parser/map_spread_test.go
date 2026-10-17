package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
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
		{"%{\n  ..m\n  \"b\" => 2\n}", "*ast.Ident", 1},
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
			if lit.Spread == nil {
				t.Fatalf("expected a spread head, got none")
			}
			if got := typeString(lit.Spread); got != tc.spread {
				t.Errorf("spread head type = %s, want %s", got, tc.spread)
			}
			if len(lit.Entries) != tc.entries {
				t.Errorf("entries = %d, want %d", len(lit.Entries), tc.entries)
			}
			if lit.TypeName != nil {
				t.Errorf("a spread literal is always anonymous, got TypeName %v", lit.TypeName)
			}
			if lit.SpreadLine == 0 || lit.SpreadCol == 0 {
				t.Errorf("spread position unset; the diagnostics and hover need it")
			}
		})
	}
}

// `{..m}` names nothing that says map, so it parses as the struct spread;
// the checker reads the base's type and words its error for a map.
func TestParseMapSpreadAloneIsTheStructForm(t *testing.T) {
	expr := parseExpr(t, "{..m}")
	if lit, ok := expr.(*ast.StructLit); !ok || lit.Spread == nil {
		t.Fatalf("expected a struct spread for `{..m}`, got %T", expr)
	}
}

func TestParseMapSpreadRejectsBadPositions(t *testing.T) {
	cases := map[string]struct {
		src       string
		want      string
		line, col int
	}{
		"after an entry":         {`%{"a" => 1, ..m}`, "map spread `..` must be the first element", 1, 17},
		"second spread later":    {`%{..m, "a" => 1, ..n}`, "map spread `..` must be the first element", 1, 22},
		"two spreads then entry": {`%{..m, ..n, "a" => 1}`, "map spread `..` must be the first element", 1, 12},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex("x = " + tc.src))
			if err == nil {
				t.Fatalf("expected a parse error for %q", tc.src)
			}
			pe, ok := err.(ParseError)
			if !ok {
				t.Fatalf("error is %T, want ParseError: %v", err, err)
			}
			if pe.Message != tc.want || pe.Line != tc.line || pe.Col != tc.col {
				t.Errorf("error = %d:%d %q, want %d:%d %q", pe.Line, pe.Col, pe.Message, tc.line, tc.col, tc.want)
			}
		})
	}
}
