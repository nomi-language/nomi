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
			"enum E {\n  Full(\n    // c\n    Int)\n}\n",
			"enum E {\n    Full (\n        // c\n        Int\n    )\n}\n",
		},
		{
			"after the payload",
			"enum E {\n  Full(Int // c\n  )\n}\n",
			"enum E {\n    Full (\n        Int // c\n    )\n}\n",
		},
		{
			"below the payload",
			"enum E {\n  Full(\n    Int\n    // c\n  )\n}\n",
			"enum E {\n    Full (\n        Int\n        // c\n    )\n}\n",
		},
		{
			"after the opening parenthesis",
			"enum E {\n  Full( // c\n    Int)\n}\n",
			"enum E {\n    Full (\n        // c\n        Int\n    )\n}\n",
		},
		{
			"a generic payload",
			"enum E {\n  Full(\n    // c\n    List<Int>)\n}\n",
			"enum E {\n    Full (\n        // c\n        List<Int>\n    )\n}\n",
		},
		{
			"a generic payload's argument",
			"enum E {\n  Full List<\n    // c\n    Int>\n}\n",
			"enum E {\n    Full List<\n        // c\n        Int\n    >\n}\n",
		},
		{
			"between two payload types",
			"enum E {\n  Pair(\n    Int, // a\n    String)\n}\n",
			"enum E {\n    Pair (\n        Int, // a\n        String\n    )\n}\n",
		},
		{
			"before a tuple payload's second type",
			"enum E {\n  Pair(Int,\n    // b\n    String)\n}\n",
			"enum E {\n    Pair (\n        Int,\n        // b\n        String\n    )\n}\n",
		},
		{
			"a parenthesized tuple payload",
			"enum E {\n  Pair((\n    // a\n    Int, String))\n}\n",
			"enum E {\n    Pair (\n        // a\n        Int,\n        String\n    )\n}\n",
		},
		{
			"a comment around a parenthesized tuple payload",
			"enum E {\n  Pair(\n    // a\n    (Int, String))\n}\n",
			"enum E {\n    Pair (\n        // a\n        (Int, String)\n    )\n}\n",
		},
		{
			"a struct-shaped variant",
			"enum E {\n  S {\n    // s\n    x: Int // x\n  }\n}\n",
			"enum E {\n    S {\n        // s\n        x: Int, // x\n    }\n}\n",
		},
		{
			"an embeds line",
			"enum E {\n  // before\n  embeds F // after\n}\n\nenum F {\n  A\n}\n",
			"enum E {\n    // before\n    embeds F // after\n}\n\nenum F {\n    A\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
