package format

import "testing"

// A positional variant's payload written in parentheses, `Full(T)`, is the
// payload `T`, and nomi fmt writes it `Full T`. A comment inside those
// parentheses is the payload type's item comments; the parentheses stay,
// with the type on its own line, so the comment keeps its place. A tuple or
// generic payload holding a comment is a list of its own and already kept
// it, as did a struct-shaped variant and an `embeds` line.
func TestFormat_EnumPayloadComment(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"before the payload",
			"enum E\n  Full(\n    // c\n    Int)\nend\n",
			"enum E\n    Full (\n        // c\n        Int\n    )\nend\n",
		},
		{
			"after the payload",
			"enum E\n  Full(Int // c\n  )\nend\n",
			"enum E\n    Full (\n        Int // c\n    )\nend\n",
		},
		{
			"below the payload",
			"enum E\n  Full(\n    Int\n    // c\n  )\nend\n",
			"enum E\n    Full (\n        Int\n        // c\n    )\nend\n",
		},
		{
			"after the opening parenthesis",
			"enum E\n  Full( // c\n    Int)\nend\n",
			"enum E\n    Full (\n        // c\n        Int\n    )\nend\n",
		},
		{
			"a generic payload",
			"enum E\n  Full(\n    // c\n    List<Int>)\nend\n",
			"enum E\n    Full (\n        // c\n        List<Int>\n    )\nend\n",
		},
		{
			"a generic payload's argument",
			"enum E\n  Full List<\n    // c\n    Int>\nend\n",
			"enum E\n    Full List<\n        // c\n        Int\n    >\nend\n",
		},
		{
			"between two payload types",
			"enum E\n  Pair(\n    Int, // a\n    String)\nend\n",
			"enum E\n    Pair (\n        Int, // a\n        String\n    )\nend\n",
		},
		{
			"before a tuple payload's second type",
			"enum E\n  Pair(Int,\n    // b\n    String)\nend\n",
			"enum E\n    Pair (\n        Int,\n        // b\n        String\n    )\nend\n",
		},
		{
			"a parenthesized tuple payload",
			"enum E\n  Pair((\n    // a\n    Int, String))\nend\n",
			"enum E\n    Pair (\n        // a\n        Int,\n        String\n    )\nend\n",
		},
		{
			"a comment around a parenthesized tuple payload",
			"enum E\n  Pair(\n    // a\n    (Int, String))\nend\n",
			"enum E\n    Pair (\n        // a\n        (Int, String)\n    )\nend\n",
		},
		{
			"a struct-shaped variant",
			"enum E\n  S {\n    // s\n    x: Int // x\n  }\nend\n",
			"enum E\n    S {\n        // s\n        x: Int, // x\n    }\nend\n",
		},
		{
			"an embeds line",
			"enum E\n  // before\n  embeds F // after\nend\n\nenum F\n  A\nend\n",
			"enum E\n    // before\n    embeds F // after\nend\n\nenum F\n    A\nend\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
