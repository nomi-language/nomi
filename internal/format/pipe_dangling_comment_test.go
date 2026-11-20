package format

import "testing"

// A comment inside a destructuring statement's pattern is the pattern's,
// written inside it as the same pattern writes it in a `case` arm, and a
// pipeline after the statement's `=` is laid out as any binding's value is.
func TestFormat_ACommentInADestructuringPatternStaysInIt(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main()\n    A(// c\n    a) = x |> f\nend\n",
			"fn main()\n    A(\n        // c\n        a\n    ) = x |> f\nend\n",
		},
		{
			"fn main()\n    A(// c\n    a) = x\n    |> f\n    |> g\nend\n",
			"fn main()\n    A(\n        // c\n        a\n    ) =\n        x\n        |> f\n        |> g\nend\n",
		},
		{
			"begin\nA((//0\na))=0|>\"\"\nend\n",
			"begin\n    A(\n        //0\n        a\n    ) = 0 |> \"\"\nend\n",
		},
		{
			"begin\nA((//0\na))=0\n|>0//\nend\n",
			"begin\n    A(\n        //0\n        a\n    ) =\n        0\n        |> 0 //\nend\n",
		},
		{
			"fn main()\n    g(fn x ->\n        A(// c\n        a) = x\n            |> f // d\n    end)\nend\n",
			"fn main()\n    g(fn x ->\n        A(\n            // c\n            a\n        ) =\n            x\n            |> f // d\n    end)\nend\n",
		},
		{
			"fn main()\n    case v\n        A(// c\n        a) -> 1\n    end\nend\n",
			"fn main()\n    case v\n        A(\n            // c\n            a\n        ) -> 1\n    end\nend\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
