package format

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// formatsKeepingMeaning formats src, requires want, a fixed point, and the
// meaning src had (SameMeaning).
func formatsKeepingMeaning(t *testing.T, src, want string) {
	t.Helper()
	formatsTo(t, src, want)
	if err := SameMeaning(src, want); err != nil {
		t.Fatal(err)
	}
}

// A parenthesized name in a destructuring's slot is that name: the parser
// builds the same destructure with or without the parentheses, so the
// formatter dropping them keeps the meaning. `{"host" => (host)} = config`
// used to be a pattern binding with a map pattern (rejected without `else`)
// and became a map destructure once formatted.
func TestFormat_ParenthesizedDestructureNameIsTheName(t *testing.T) {
	cases := []struct {
		src, want string
		node      ast.Node
	}{
		{
			"fn main() {\n    {\"host\" => (host)} = config\n}\n",
			"fn main() {\n    {\"host\" => host} = config\n}\n",
			&ast.MapDestructure{},
		},
		{
			"fn main() {\n    {\"host\" => ((_))} = config\n}\n",
			"fn main() {\n    {\"host\" => _} = config\n}\n",
			&ast.MapDestructure{},
		},
		{
			"fn main() {\n    {x: (a), y} = p\n}\n",
			"fn main() {\n    {x: a, y} = p\n}\n",
			&ast.StructDestructure{},
		},
		{
			"fn main() {\n    ((a), _) = t\n}\n",
			"fn main() {\n    (a, _) = t\n}\n",
			&ast.TupleDestructure{},
		},
		{
			"fn main() {\n    Day((n)) = d\n}\n",
			"fn main() {\n    Day(n) = d\n}\n",
			&ast.DistinctDestructure{},
		},
		{
			"fn main() {\n    Day.Hours((n)) = h\n}\n",
			"fn main() {\n    Day.Hours(n) = h\n}\n",
			&ast.DistinctDestructure{},
		},
	}
	for _, c := range cases {
		nodes, err := parser.Parse(lexer.Lex(c.src))
		if err != nil {
			t.Fatalf("the parser rejects %q: %v", c.src, err)
		}
		stmt := nodes[0].(*ast.FuncDef).Body.Stmts[0]
		if got, want := stmt.NodeType(), c.node.NodeType(); got != want {
			t.Errorf("%q parses as %s, want %s", c.src, got, want)
		}
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
