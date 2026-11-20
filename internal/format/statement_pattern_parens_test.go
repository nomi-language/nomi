package format

import (
	"testing"
)

// A statement's pattern needs no parentheses: written bare, every pattern
// but a name or `_` alone is the same pattern binding, and a name or `_`
// alone is the plain binding.
func TestFormat_StatementPatternDropsParenthesesItsParseDoesNotNeed(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn main()\n    (A(a)) = 0\nend\n", "fn main()\n    A(a) = 0\nend\n"},
		{"fn main()\n    ((A((a)))) = 0\nend\n", "fn main()\n    A(a) = 0\nend\n"},
		{"fn main()\n    (A(_)) = 0\nend\n", "fn main()\n    A(_) = 0\nend\n"},
		{"fn main()\n    (m.A(a)) = 0\nend\n", "fn main()\n    m.A(a) = 0\nend\n"},
		{"fn main()\n    (A.B(a)) = 0\nend\n", "fn main()\n    A.B(a) = 0\nend\n"},
		{"begin\n(A(a))=\"\"\nend", "begin\n    A(a) = \"\"\nend\n"},
		{"begin\n(A(// c\na))=\"\"\nend", "begin\n    A(\n        // c\n        a\n    ) = \"\"\nend\n"},
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
		{"fn main()\n    ((a, b)) = t\nend\n", "fn main()\n    (a, b) = t\nend\n"},
		{"fn main()\n    ((a, (b), _)) = t\nend\n", "fn main()\n    (a, b, _) = t\nend\n"},
		{"fn main()\n    ({x:}) = p\nend\n", "fn main()\n    {x:} = p\nend\n"},
		{"fn main()\n    ({x: (n), y:}) = p\nend\n", "fn main()\n    {x: n, y:} = p\nend\n"},
		{"begin\n((a,a))=\"\"\nend", "begin\n    (a, a) = \"\"\nend\n"},
		// A comment inside the pattern breaks it.
		{"begin\n((//\na,a))=\"\"\nend", "begin\n    (\n        //\n        a,\n        a\n    ) = \"\"\nend\n"},
		{"begin\n((a, // c\na))=\"\"\nend", "begin\n    (\n        a, // c\n        a\n    ) = \"\"\nend\n"},
		{"begin\n(({// c\nx:})) = p\nend", "begin\n    {\n        // c\n        x:,\n    } = p\nend\n"},
		{"fn main()\n    (_) = t\nend\n", "fn main()\n    _ = t\nend\n"},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
