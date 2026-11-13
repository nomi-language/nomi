package format

import "testing"

// A comment on a declaration's header line, with a blank line after it,
// moves below the header and the blank line goes, in one pass: the moved
// comment is on a line of its own, and the lexer keeps no blank line after
// a comment line, so a blank line kept the first time was dropped by the
// second and formatting a formatted file changed it again.
func TestFormat_HeaderCommentDropsTheBlankLineAfterIt(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"impl": {
			"impl Farewell for Person // note\n" +
				"\n" +
				"    fn greet(p: Person) -> String\n" +
				"        p.name\n" +
				"    end\n" +
				"end\n",
			"impl Farewell for Person\n" +
				"    // note\n" +
				"    fn greet(p: Person) -> String\n" +
				"        p.name\n" +
				"    end\n" +
				"end\n",
		},
		"interface": {
			"interface Named // note\n" +
				"\n" +
				"    fn name(value: self) -> String\n" +
				"end\n",
			"interface Named\n" +
				"    // note\n" +
				"    fn name(value: self) -> String\n" +
				"end\n",
		},
		"function": {
			"fn f() -> Int // note\n" +
				"\n" +
				"    1\n" +
				"end\n",
			"fn f() -> Int\n" +
				"    // note\n" +
				"    1\n" +
				"end\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := formatTwice(t, c.src)
			if got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if err := SameMeaning(c.src, got); err != nil {
				t.Fatalf("%v\nformatted:\n%s", err, got)
			}
		})
	}
}
