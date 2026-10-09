package format

import "testing"

// A comma-separated braced form (struct, list, map, set and vector
// literals, anonymous struct types, struct-shaped variants) is written with
// a comma after every entry. The lexer emits no blank-line token after a
// comma, so a blank line before a comment that closes such a form cannot
// survive a second format. It is dropped on the first one too, as blank
// lines between the entries already are.
func TestFormat_BracedEndTrivia_DropsBlankLines(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"anon struct literal": {
			"fn main() {\n    x = {a: 0 / 0\n\n    //\n    }\n}\n",
			"fn main() {\n    x = {\n        a: 0 / 0,\n        //\n    }\n}\n",
		},
		"named struct literal": {
			"fn main() {\n    x = P{a: 0\n\n    // c\n\n    // d\n    }\n}\n",
			"fn main() {\n    x = P{\n        a: 0,\n        // c\n        // d\n    }\n}\n",
		},
		"map literal": {
			"fn main() {\n    m = {1 => 2\n\n    // c\n    }\n}\n",
			"fn main() {\n    m = {\n        1 => 2,\n        // c\n    }\n}\n",
		},
		"anon struct type": {
			"fn f(p: {x: Int\n\n// c\n}): Int {\n    1\n}\n",
			"fn f(p: {\n    x: Int,\n    // c\n}): Int {\n    1\n}\n",
		},
		"struct variant": {
			"enum Shape {\n    Rect {w: Int\n\n    // c\n    }\n}\n",
			"enum Shape {\n    Rect {\n        w: Int,\n        // c\n    }\n}\n",
		},
	} {
		t.Run(name, func(t *testing.T) { migrates(t, c.src, c.want) })
	}
}
