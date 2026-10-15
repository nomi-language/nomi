package format

import "testing"

// A comment waiting for the end of its line (LineSuffix) cannot end a line
// that ends in a binding's `=`: `A(0) = // c` does not parse. It ends the
// value's first line instead. The comment inside the pattern below is a
// dangling comment of the pattern's `0`, written after it, and the pipeline
// value breaks after the `=`.
func TestFormat_CommentDoesNotEndABindingsEqualsLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    A((// c\n    0)) = 0\n    |> f\n}\n",
			"fn main() {\n    A(0) =\n        0 // c\n        |> f\n}\n",
		},
		{
			"{A((//0\n0))=0\n|>0/00}\n",
			"{\n    A(0) =\n        0 //0\n        |> 0 / 00\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
