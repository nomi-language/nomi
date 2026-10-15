package format

import "testing"

// A comment the parser gives a pipeline as a dangling comment survives
// where the formatter writes the pipeline itself rather than through emit,
// as after a binding's `=`. There it ends the pipeline's last line, since
// nothing may come between `=` and the value. A comment inside a
// destructuring pattern before the `=` is such a comment: `A(// c` `a) = x
// |> f` used to lose it.
func TestFormat_PipelineKeepsItsDanglingComments(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    A(// c\n    a) = x |> f\n}\n",
			"fn main() {\n    A(a) = x |> f // c\n}\n",
		},
		{
			"fn main() {\n    A(// c\n    a) = x\n    |> f\n    |> g\n}\n",
			"fn main() {\n    A(a) =\n        x\n        |> f\n        |> g // c\n}\n",
		},
		{
			"{A((//0\na))=0|>\"\"}\n",
			"{ A(a) = 0 |> \"\" } //0\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
