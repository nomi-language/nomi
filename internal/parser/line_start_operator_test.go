package parser

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A binary operator at the start of a line below an operand is an error at
// the operator that says the line break ended the expression and how to
// continue it, for every operator that only joins two operands, in a
// statement and inside parentheses.
func TestBinaryOperatorStartingALine(t *testing.T) {
	ops := []string{"==", "!=", "<", ">", "<=", ">=", "and", "or", "+", "*", "/", "%"}
	shapes := []struct {
		name, format string
		line, col    int
	}{
		{"assert", "fn f() {\n    assert 1 + 1\n        %s 2\n}", 3, 9},
		{"binding", "fn f() -> Bool {\n    x = g(1)\n    %s 2\n}", 3, 5},
		{"after a comment", "fn f() -> Bool {\n    x = #[1, 2] // pair\n        %s 2\n}", 3, 9},
		{"parentheses", "fn f() -> Bool {\n    (1 + 1\n        %s 2)\n}", 3, 9},
	}
	for _, shape := range shapes {
		for _, op := range ops {
			src := fmt.Sprintf(shape.format, op)
			t.Run(shape.name+" "+op, func(t *testing.T) {
				err := parseError(t, src)
				var pe ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("error is %T (%v), want ParseError", err, err)
				}
				if want := "`" + op + "` cannot start a line: the line break ended the expression before it"; pe.Message != want {
					t.Errorf("message %q, want %q", pe.Message, want)
				}
				wantHint := "put `" + op + "` at the end of the previous line, so the expression continues onto this one"
				if len(pe.Hints) != 1 || pe.Hints[0] != wantHint {
					t.Errorf("hints %q, want [%q]", pe.Hints, wantHint)
				}
				if pe.Line != shape.line || pe.Col != shape.col {
					t.Errorf("error at %d:%d, want %d:%d (the operator)", pe.Line, pe.Col, shape.line, shape.col)
				}
			})
		}
	}
}

// The operator at the end of the line continues the expression, and `|>`
// may start a line.
func TestOperatorEndingALineAndPipeStartingOneParse(t *testing.T) {
	for _, src := range []string{
		"fn f() {\n    assert 1 + 1 ==\n        2\n}",
		"fn f() -> List<Int> {\n    [1, 2]\n    |> Iter.to_list()\n}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

// An operator that opens an expression on its own line, with no operand
// above it, says it needs an operand; any other unexpected token is shown
// as written, never by the lexer's name for it.
func TestUnexpectedTokensAreShownAsWritten(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn f() -> Bool {\n    x = == 2\n}", "`==` needs an operand before it"},
		{"fn f() {\n    {\n    == 2\n}", "`==` needs an operand before it"},
		{"fn f() -> Int {\n    x = )\n}", "unexpected `)`"},
		{"fn f(n: Int) -> Int {\n    case n {\n        => 1\n    }\n}", "unexpected `=>` in case pattern"},
		{"struct S {\n    ,\n}", "expected field name in struct `S` body, got `,`"},
		{"fn f() {\n    p = Point{}\n}", "unexpected `{` right after the end of a statement"},
		{"fn f() -> Int {\n    x = \"a${)}\"\n    1\n}", "unexpected `)`"},
	}
	for _, tc := range cases {
		err := parseError(t, tc.src)
		var pe ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%q: error is %T (%v), want ParseError", tc.src, err, err)
		}
		if pe.Message != tc.want {
			t.Errorf("%q: message %q, want %q", tc.src, pe.Message, tc.want)
		}
	}
}
