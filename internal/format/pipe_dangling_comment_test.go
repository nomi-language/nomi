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
		// When the last stage's line already ends in a comment, the
		// dangling one goes on a line of its own below the statement, at
		// the statement's indent, where a second format reads it back. It
		// used to take the stages' indent and move out on the next format.
		{
			"{A((//0\na))=0\n|>0//\n}\n",
			"{\n    A(a) =\n        0\n        |> 0 //\n    //0\n}\n",
		},
		{
			"fn main() {\n    g(|x| {\n        A(// c\n        a) = x\n            |> f // d\n    })\n}\n",
			"fn main() {\n    g(|x| {\n        A(a) =\n            x\n            |> f // d\n        // c\n    })\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
