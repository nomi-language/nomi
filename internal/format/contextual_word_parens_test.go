package format

import "testing"

// A name the parser reads as a keyword when it ends its line keeps a pair of
// parentheses, since the formatter puts each statement on its own line.
// `group` alone on a top-level line opens a test group without a name, so a
// top-level statement that is the name `group` is written `(group)`; in a
// body that line reads the name and stays bare. `concurrent` ending its line
// anywhere opens a concurrent block, so the name is always written
// `(concurrent)`.
func TestFormat_ContextualWordKeepsParens(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"group []\n", "(group)\n\n[]\n"},
		{"group; \"x\"\n", "(group)\n\n\"x\"\n"},
		{"((group))\n", "(group)\n"},
		{"// a\ngroup; 1 // b\n", "// a\n(group)\n\n1 // b\n"},
		{"fn f()\n    group; 1\nend\n", "fn f()\n    group\n    1\nend\n"},
		{"fn f()\n    concurrent; 1\nend\n", "fn f()\n    (concurrent)\n    1\nend\n"},
		{"fn f()\n    x = ((concurrent))\n    x\nend\n", "fn f()\n    x = (concurrent)\n    x\nend\n"},
		{"fn f()\n    x = 1 + concurrent; 2\nend\n", "fn f()\n    x = 1 + (concurrent)\n    2\nend\n"},
		{"x |> tap concurrent; 1\n", "x |> tap (concurrent)\n\n1\n"},
		{"fn f()\n    x |> tap concurrent; 1\nend\n", "fn f()\n    x |> tap (concurrent)\n    1\nend\n"},
		{"fn f()\n    x |> tap ((concurrent)) |> g()\nend\n", "fn f()\n    x |> tap (concurrent) |> g()\nend\n"},
		{"fn f()\n    x |> tap concurrent.log; 1\nend\n", "fn f()\n    x |> tap (concurrent).log\n    1\nend\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
		formatsTo(t, c.want, c.want)
	}
}
