package format

import "testing"

// A pipe stage's named `_` stays where it is written, in a one-line stage,
// a stacked pipeline, and a stage a trailing `try` moves into.
func TestFormat_PipeNamedHoleStays(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    s = \"x\" |> tag(1,w:_)\n}\n",
			"fn main() {\n    s = \"x\" |> tag(1, w: _)\n}\n",
		},
		{
			"fn main() {\n    s = \"x\"\n    |> tag(w: _, a: 2)\n    |> String.pad_start(width: 6, s: _)\n}\n",
			"fn main() {\n    s =\n        \"x\"\n        |> tag(w: _, a: 2)\n        |> String.pad_start(width: 6, s: _)\n}\n",
		},
		{
			"fn main() {\n    n = 8 |> halve(by: 2, n: _) |> try\n}\n",
			"fn main() {\n    n = 8 |> try halve(by: 2, n: _)\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
