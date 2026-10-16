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
		{"fn main() {\n    (A(a)) = 0\n}\n", "fn main() {\n    (A(a)) = 0\n}\n"},
		{"fn main() {\n    ((A((a)))) = 0\n}\n", "fn main() {\n    (A(a)) = 0\n}\n"},
		{"fn main() {\n    (A(_)) = 0\n}\n", "fn main() {\n    (A(_)) = 0\n}\n"},
		{"fn main() {\n    (m.A(a)) = 0\n}\n", "fn main() {\n    (m.A(a)) = 0\n}\n"},
		{"fn main() {\n    (A.B(a)) = 0\n}\n", "fn main() {\n    (A.B(a)) = 0\n}\n"},
		{"fn main() {\n    ((a, b)) = t\n}\n", "fn main() {\n    ((a, b)) = t\n}\n"},
		{"fn main() {\n    ((a, (b), _)) = t\n}\n", "fn main() {\n    ((a, b, _)) = t\n}\n"},
		{"fn main() {\n    ({x}) = p\n}\n", "fn main() {\n    ({x}) = p\n}\n"},
		{"fn main() {\n    ({x: (n), y}) = p\n}\n", "fn main() {\n    ({x: n, y}) = p\n}\n"},
		// Inside a block that is a value.
		{"{((a,a))=\"\"}", "{ ((a, a)) = \"\" }\n"},
		// A comment inside the pattern breaks it inside the parentheses.
		{"{((//\na,a))=\"\"}", "{ ((\n    //\n    a,\n    a\n)) = \"\" }\n"},
		{"{((a, // c\na))=\"\"}", "{ ((\n    a, // c\n    a\n)) = \"\" }\n"},
		{"{(A(// c\na))=\"\"}", "{ (A(\n    // c\n    a\n)) = \"\" }\n"},
		{"{(({// c\nx})) = p}", "{ ({\n    // c\n    x,\n}) = p }\n"},
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
		{"fn main() {\n    ((a, A(b))) = t\n}\n", "fn main() {\n    (a, A(b)) = t\n}\n"},
		{"fn main() {\n    (A(a, b)) = t\n}\n", "fn main() {\n    A(a, b) = t\n}\n"},
		{"fn main() {\n    (.Some(a)) = t\n}\n", "fn main() {\n    .Some(a) = t\n}\n"},
		{"fn main() {\n    (P{x}) = t\n}\n", "fn main() {\n    P{x} = t\n}\n"},
		{"fn main() {\n    ({x: _}) = t\n}\n", "fn main() {\n    {x: _} = t\n}\n"},
		{"fn main() {\n    ({\"k\" => A(a)}) = t\n}\n", "fn main() {\n    {\"k\" => A(a)} = t\n}\n"},
		{"fn main() {\n    ({\"k\" => v}) = m\n}\n", "fn main() {\n    {\"k\" => v} = m\n}\n"},
		{"fn main() {\n    ({\"k\" => _}) = m\n}\n", "fn main() {\n    {\"k\" => _} = m\n}\n"},
		{"fn main() {\n    (A(a) as b) = t\n}\n", "fn main() {\n    A(a) as b = t\n}\n"},
		{"fn main() {\n    (A(a)) = t else { return }\n}\n", "fn main() {\n    A(a) = t else { return }\n}\n"},
		{"fn main() {\n    ((a, b)) = t else { return }\n}\n", "fn main() {\n    (a, b) = t else { return }\n}\n"},
		{"fn main() {\n    ((a)) = t\n}\n", "fn main() {\n    a = t\n}\n"},
		{"fn main() {\n    (_) = t\n}\n", "fn main() {\n    _ = t\n}\n"},
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
