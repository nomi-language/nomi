package format

import "testing"

// A group's fits check counts what follows it on its line, and a group
// there that will break writes its broken form's first line: a lambda that
// moves its body below the parameters ends the line with ` {`. The check
// counted the lambda's flat form up to its first possible break, without
// the ` {`, so a function type's parameter list stayed on a line one column
// too wide, and the next format, reading the lambda's block back as
// written, broke the parameter list instead.
func TestFormat_TailGroupThatBreaksCountsItsBrokenFirstLine(t *testing.T) {
	cases := []struct{ src, want string }{
		{
			"{a00000000000000000000000000:(A00)->A00=|A000000000000000000000000000000000000000000000000|\"\"}",
			"{\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00\n" +
				"    ) -> A00 = |A000000000000000000000000000000000000000000000000| \"\"\n" +
				"}\n",
		},
		{
			// The same binding with the lambda's body written as a block
			// below its parameters.
			"{a00000000000000000000000000:(A00)->A00=|A000000000000000000000000000000000000000000000000| {\n\"\"\n}}",
			"{\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00\n" +
				"    ) -> A00 = |A000000000000000000000000000000000000000000000000| {\n" +
				"        \"\"\n" +
				"    }\n" +
				"}\n",
		},
		{
			"{a00000000000000000000000000:(A00, B00)->A00=|A000000000000000000000000000000000000000000000|\"\"}",
			"{\n" +
				"    a00000000000000000000000000: (\n" +
				"        A00,\n" +
				"        B00\n" +
				"    ) -> A00 = |A000000000000000000000000000000000000000000000| \"\"\n" +
				"}\n",
		},
		{
			// Short enough, everything stays on one line.
			"{a00000000000000000000000:(A00)->A00=|A000000000000000000000000000000000000000000000000|\"\"}",
			"{ a00000000000000000000000: (A00) -> A00 = |A000000000000000000000000000000000000000000000000| \"\" }\n",
		},
		{
			// A value that is not a lambda breaks itself.
			"fn f(x: Int) {\n    a00000000000000000000000000: (A00) -> A00 = |x| fffffffffffffffffffffffffffffffffffffff(xxxxxxxxxxxxxxxxxxxxxxxxxxxxx)\n}\n",
			"fn f(x: Int) {\n    a00000000000000000000000000: (A00) -> A00 = |x| {\n        fffffffffffffffffffffffffffffffffffffff(xxxxxxxxxxxxxxxxxxxxxxxxxxxxx)\n    }\n}\n",
		},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
		formatsTo(t, c.want, c.want)
	}
}
