package format

import "testing"

// A comment on a declaration's header line, with a blank line after it,
// moves below the header and the blank line goes, in one pass: the moved
// comment is on a line of its own, and the lexer keeps no blank line after
// a comment line, so a blank line kept the first time was dropped by the
// second and formatting a formatted file changed it again. The comment
// lands in a statement's leading comments, a member's, or the comments
// before `end` when the body holds nothing else, and each slot drops the
// blank line (withoutBlankAfterComment). FuzzFormatKeepsMeaning found the
// last on `fn A()//\n\n//0\nend`.
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
		"function holding only comments": {
			"fn f() // note\n\n    // more\nend\n",
			"fn f()\n    // note\n    // more\nend\n",
		},
		"nested function holding only comments": {
			"fn f()\n    fn g() // note\n\n        // more\n    end\n    g()\nend\n",
			"fn f()\n    fn g()\n        // note\n        // more\n    end\n\n    g()\nend\n",
		},
		"impl holding only comments": {
			"impl Marker for P // note\n\n    // more\nend\n",
			"impl Marker for P\n    // note\n    // more\nend\n",
		},
		"interface holding only comments": {
			"interface Named // note\n\n    // more\nend\n",
			"interface Named\n    // note\n    // more\nend\n",
		},
		"struct, a field next": {
			"struct P // note\n\n    n: Int\nend\n",
			"struct P\n    // note\n    n: Int\nend\n",
		},
		"struct, a comment and a field next": {
			"struct P // note\n\n    // more\n    n: Int\nend\n",
			"struct P\n    // note\n    // more\n    n: Int\nend\n",
		},
		"struct holding only comments": {
			"struct P // note\n\n    // more\nend\n",
			"struct P\n    // note\n    // more\nend\n",
		},
		"enum, a variant next": {
			"enum E // note\n\n    A\nend\n",
			"enum E\n    // note\n    A\nend\n",
		},
		"enum, a comment and a variant next": {
			"enum E // note\n\n    // more\n    A\nend\n",
			"enum E\n    // note\n    // more\n    A\nend\n",
		},
		"enum, a struct variant next": {
			"enum E // note\n\n    A{x: Int}\nend\n",
			"enum E\n    // note\n    A {x: Int}\nend\n",
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
