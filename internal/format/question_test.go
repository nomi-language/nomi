package format

import "testing"

// A `?` is written against the expression it follows, in a pipe stage too,
// and `then?` keeps its `?` on the keyword.
func TestFormat_QuestionStaysGlued(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"a = f(x) ?\n", "a = f(x)?\n"},
		{"a = load()?.name\n", "a = load()?.name\n"},
		{"a = f()!?\n", "a = f()!?\n"},
		{"a = text |> parse() ? |> validate()?\n", "a = text |> parse()? |> validate()?\n"},
		{
			"a =\n    text\n    |> parse()?\n    |> validate()?\n",
			"a =\n    text\n    |> parse()?\n    |> validate()?\n",
		},
		{
			"a = x |> then? fn v -> parse(v) |> dbg\n",
			"a =\n    x\n    |> then? fn v -> parse(v)\n    |> dbg\n",
		},
		{"a = x |> then? &parse(&1)\n", "a = x |> then? &parse(&1)\n"},
		{
			"a = x |> case { _ -> f() }?\n",
			"a = x |> case {\n    _ -> f()\n}?\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
