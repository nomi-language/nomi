package parser

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An arithmetic operator at the start of a line below an operand is an
// error at the operator that says the line break ended the expression and
// how to continue it, in a statement and inside parentheses. A comparison
// or boolean operator continues the expression instead
// (TestComparisonOrBooleanOperatorContinuesALine).
func TestBinaryOperatorStartingALine(t *testing.T) {
	ops := []string{"+", "*", "/", "%"}
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

// A comparison or boolean operator starting a line continues the expression
// on the line above, as `|>` does, since no expression starts with one: in
// an assertion, a binding, after a comment and inside parentheses.
func TestComparisonOrBooleanOperatorContinuesALine(t *testing.T) {
	ops := []string{"==", "!=", "<", ">", "<=", ">=", "and", "or"}
	for _, format := range []string{
		"fn f() {\n    assert 1 + 1\n        %s 2\n}",
		"fn f() -> Bool {\n    x = g(1)\n        %s 2\n    x\n}",
		"fn f() -> Bool {\n    x = g(1) // first\n        %s 2\n    x\n}",
		"fn f() -> Bool {\n    (1 + 1\n        %s 2)\n}",
	} {
		for _, op := range ops {
			src := fmt.Sprintf(format, op)
			if _, err := Parse(lexer.Lex(src)); err != nil {
				t.Errorf("Parse(%q): %v", src, err)
			}
		}
	}
}

// A line that starts with a name beginning with `and` or `or` (`order`,
// `or?`) starts a new statement.
func TestNameStartingWithAndOrOrStartsAStatement(t *testing.T) {
	for _, src := range []string{
		"fn f(order: Int) -> Int {\n    x = 1\n    order\n}",
		"fn f() -> Bool {\n    x = g(1)\n    or?(x)\n}",
		"fn f(android: Int) -> Int {\n    x = 1\n    android\n}",
	} {
		nodes, err := Parse(lexer.Lex(src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		fn := nodes[0].(*ast.FuncDef)
		if len(fn.Body.Stmts) != 2 {
			t.Errorf("Parse(%q): %d statements, want 2", src, len(fn.Body.Stmts))
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
