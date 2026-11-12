package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A named struct literal keeps the values written with no field name in
// Positional, each with its text and its first token's position, for the
// checker's error; a name alone is still a punned field.
func TestPositionalStructLit_Recovered(t *testing.T) {
	lit, ok := parseExpr(t, `Student{name:, &1.second, f(x) + 1}`).(*ast.StructLit)
	if !ok {
		t.Fatal("want a struct literal")
	}
	if len(lit.Fields) != 1 || lit.Fields[0].Name != "name" {
		t.Fatalf("fields = %+v; want the punned `name`", lit.Fields)
	}
	if len(lit.Positional) != 2 {
		t.Fatalf("positional = %+v; want two values", lit.Positional)
	}
	for i, want := range []struct {
		source string
		col    int
	}{{"&1.second", 16}, {"f(x) + 1", 27}} {
		p := lit.Positional[i]
		if p.Name != "" || p.Source != want.source || p.Line != 1 || p.Col != want.col {
			t.Errorf("positional %d = %q at 1:%d (name %q); want %q at 1:%d", i, p.Source, p.Col, p.Name, want.source, want.col)
		}
	}
}

// What is no value stays the parse error it was, and a missing comma
// between values is reported as one.
func TestPositionalStructLit_ParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"Point{x = 1}":   "expected `,` or `}` after the field `x`",
		"Point{=}":       "expected field name in struct literal",
		"Point{a.b c.d}": "expected `,` or `}` after the value",
		"{1, 2}":         "a `{` opens a struct, whose fields are `name: value`, or a map, whose entries are `key => value`: a tuple is `(a, b)`, and a set is `[a, b]` where one is expected, else `Set[a, b]`",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil || !containsMessage(err, want) {
			t.Errorf("Parse(%q) = %v; want %q", src, err, want)
		}
	}
}

func containsMessage(err error, want string) bool {
	pe, ok := err.(ParseError)
	if !ok {
		return false
	}
	return pe.Message == want
}
