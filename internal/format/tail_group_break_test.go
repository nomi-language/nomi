package format

import "testing"

// A group's fits check counts what follows it on its line, and a group
// there that will break writes its broken form's first line: a lambda that
// moves its body below the parameters ends the line with ` {`. The check
// counted the lambda's flat form up to its first possible break, without
// the line break after its `->`, so a function type's parameter list stayed on a line one column
// too wide, and the next format, reading the lambda's block back as
// written, broke the parameter list instead.
func TestFormat_TailGroupThatBreaksCountsItsBrokenFirstLine(t *testing.T) {
	cases := []struct{ src, want string }{
		{
			"begin\na00000000000000000000000000:(A00)->A00=fn A000000000000000000000000000000000000000000000000 -> \"\"\nend",
			"begin\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00\n" +
				"    ) -> A00 = fn A000000000000000000000000000000000000000000000000 -> \"\"\n" +
				"end\n",
		},
		{
			// The same binding with the lambda's body written as a block
			// below its parameters.
			"begin\n    a00000000000000000000000000:(A00)->A00=fn A000000000000000000000000000000000000000000000000 ->\n\"\"\nend\nend",
			"begin\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00\n" +
				"    ) -> A00 = fn A000000000000000000000000000000000000000000000000 ->\n" +
				"        \"\"\n" +
				"    end\n" +
				"end\n",
		},
		{
			"begin\na00000000000000000000000000:(A00, B00)->A00=fn A000000000000000000000000000000000000000000000 -> \"\"\nend",
			"begin\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00,\n" +
				"        B00\n" +
				"    ) -> A00 = fn A000000000000000000000000000000000000000000000 -> \"\"\n" +
				"end\n",
		},
		{
			// Short enough, everything stays on one line.
			"begin\na00000000000000000000000:(A00)->A00=fn A000000000000000000000000000000000000000000000 -> \"\"\nend",
			"begin\n    a00000000000000000000000: (A00) -> A00 = fn A000000000000000000000000000000000000000000000 -> \"\"\nend\n",
		},
		{
			// A value that is not a lambda breaks itself.
			"fn f(x: Int)\n    a00000000000000000000000000: (A00) -> A00 = fn x -> fffffffffffffffffffffffffffffffffffffff(xxxxxxxxxxxxxxxxxxxxxxxxxxxxx)\nend\n",
			"fn f(x: Int)\n    a00000000000000000000000000: (A00) -> A00 = fn x ->\n        fffffffffffffffffffffffffffffffffffffff(xxxxxxxxxxxxxxxxxxxxxxxxxxxxx)\n    end\nend\n",
		},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
		formatsTo(t, c.want, c.want)
	}
}
