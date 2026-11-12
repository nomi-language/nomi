package format

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A pattern binding without `else` whose pattern, written bare, starts a
// destructuring statement keeps one pair of parentheses: `(A(a)) = 0` is a
// pattern binding and `A(a) = 0` a distinct destructure, `((a, b)) = t` a
// pattern binding and `(a, b) = t` a tuple destructure, and the same for a
// struct pattern. The formatter dropped them, which changed the
// statement the parser reads.
func TestFormat_StatementPatternKeepsTheParenthesesItsParseNeeds(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn main()\n    (A(a)) = 0\nend\n", "fn main()\n    (A(a)) = 0\nend\n"},
		{"fn main()\n    ((A((a)))) = 0\nend\n", "fn main()\n    (A(a)) = 0\nend\n"},
		{"fn main()\n    (A(_)) = 0\nend\n", "fn main()\n    (A(_)) = 0\nend\n"},
		{"fn main()\n    (m.A(a)) = 0\nend\n", "fn main()\n    (m.A(a)) = 0\nend\n"},
		{"fn main()\n    (A.B(a)) = 0\nend\n", "fn main()\n    (A.B(a)) = 0\nend\n"},
		{"fn main()\n    ((a, b)) = t\nend\n", "fn main()\n    ((a, b)) = t\nend\n"},
		{"fn main()\n    ((a, (b), _)) = t\nend\n", "fn main()\n    ((a, b, _)) = t\nend\n"},
		{"fn main()\n    ({x:}) = p\nend\n", "fn main()\n    ({x:}) = p\nend\n"},
		{"fn main()\n    ({x: (n), y:}) = p\nend\n", "fn main()\n    ({x: n, y:}) = p\nend\n"},
		// Inside a block that is a value.
		{"begin\n((a,a))=\"\"\nend", "begin\n    ((a, a)) = \"\"\nend\n"},
		// A comment inside the pattern breaks it inside the parentheses.
		{"begin\n((//\na,a))=\"\"\nend", "begin\n    ((\n        //\n        a,\n        a\n    )) = \"\"\nend\n"},
		{"begin\n((a, // c\na))=\"\"\nend", "begin\n    ((\n        a, // c\n        a\n    )) = \"\"\nend\n"},
		{"begin\n(A(// c\na))=\"\"\nend", "begin\n    (A(\n        // c\n        a\n    )) = \"\"\nend\n"},
		{"begin\n(({// c\nx:})) = p\nend", "begin\n    ({\n        // c\n        x:,\n    }) = p\nend\n"},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
		stmt := firstStatement(t, c.want)
		if _, ok := stmt.(*ast.PatternBinding); !ok {
			t.Errorf("%q formats to a %T, want a pattern binding", c.src, stmt)
		}
	}
}

// Parentheses the parse does not need still go: a pattern that starts no
// destructuring statement (a map pattern never does), a pattern binding with `else` (which the parser
// reads as a pattern binding whatever its pattern), and a name or `_`
// alone, which is the plain binding.
func TestFormat_StatementPatternDropsParenthesesItsParseDoesNotNeed(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn main()\n    ((a, A(b))) = t\nend\n", "fn main()\n    (a, A(b)) = t\nend\n"},
		{"fn main()\n    (A(a, b)) = t\nend\n", "fn main()\n    A(a, b) = t\nend\n"},
		{"fn main()\n    (.Some(a)) = t\nend\n", "fn main()\n    .Some(a) = t\nend\n"},
		{"fn main()\n    (P{x:}) = t\nend\n", "fn main()\n    P{x:} = t\nend\n"},
		{"fn main()\n    ({x: _}) = t\nend\n", "fn main()\n    {x: _} = t\nend\n"},
		{"fn main()\n    ({\"k\" => A(a)}) = t\nend\n", "fn main()\n    {\"k\" => A(a)} = t\nend\n"},
		{"fn main()\n    ({\"k\" => v}) = m\nend\n", "fn main()\n    {\"k\" => v} = m\nend\n"},
		{"fn main()\n    ({\"k\" => _}) = m\nend\n", "fn main()\n    {\"k\" => _} = m\nend\n"},
		{"fn main()\n    (A(a) as b) = t\nend\n", "fn main()\n    A(a) as b = t\nend\n"},
		{"fn main()\n    (A(a)) = t else return\nend\n", "fn main()\n    A(a) = t else return\nend\n"},
		{"fn main()\n    ((a, b)) = t else return\nend\n", "fn main()\n    (a, b) = t else return\nend\n"},
		{"fn main()\n    ((a)) = t\nend\n", "fn main()\n    a = t\nend\n"},
		{"fn main()\n    (_) = t\nend\n", "fn main()\n    _ = t\nend\n"},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

func firstStatement(t *testing.T, src string) ast.Node {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("%q does not parse: %v", src, err)
	}
	var stmt ast.Node
	ast.Inspect(nodes[0], func(n ast.Node) bool {
		if b, ok := n.(*ast.Block); ok && stmt == nil && len(b.Stmts) > 0 {
			stmt = b.Stmts[0]
		}
		return stmt == nil
	})
	if stmt == nil {
		t.Fatalf("%q holds no block", src)
	}
	return stmt
}
