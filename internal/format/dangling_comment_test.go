package format

import "testing"

// A comment where the syntax tree has no trivia slot is a dangling comment
// on the node nearest it (parser.attachDangling), and the formatter writes
// it at the end of the line that node starts or ends on, or on a line of its
// own above it.
func TestFormat_DanglingComments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"after a parameter list's (",
			"fn double( // why\n    n: Int,\n): Int {\n    n * 2\n}\n",
			"fn double(n: Int): Int {\n    // why\n    n * 2\n}\n",
		},
		{
			"after a grouping (",
			"fn f(): Int {\n    y = try ( // why\n        g())\n    y\n}\n",
			"fn f(): Int {\n    y = try g() // why\n    y\n}\n",
		},
		{
			"inside a map pattern",
			"fn f(m: Map<String, Int>): Int {\n    case m {\n        { // the key\n            \"a\" => n,\n        } -> n\n        _ -> 0\n    }\n}\n",
			"fn f(m: Map<String, Int>): Int {\n    case m {\n        {\"a\" => n} -> n // the key\n        _ -> 0\n    }\n}\n",
		},
		{
			"after a list spread's ..",
			"xs = [1, .. // rest\n[2]]\n",
			"xs = [1, ..[2]] // rest\n",
		},
		{
			"closing a spread list",
			"fn main() {\n    list = [1, ..[] // why\n    ]\n}\n",
			"fn main() {\n    list = [1, ..[]] // why\n}\n",
		},
		{
			"after a with statement's =",
			"fn publish_quietly() {\n    with App.logger = // why\n        Silent\n    publish()\n}\n",
			"fn publish_quietly() {\n    with App.logger = Silent // why\n    publish()\n}\n",
		},
		{
			"closing a tuple",
			"fn main() {\n    t = (1, 2 // why\n    )\n    _ = t\n}\n",
			"fn main() {\n    t = (1, 2) // why\n    _ = t\n}\n",
		},
		{
			// The line already ends in a comment, so the dangling one takes
			// a line of its own, above the next line with code.
			"on a line that has a comment",
			"list = [1, .. // c1\n          [2]] // t\n\ncase list {\n    _ -> 0\n}\n",
			"list = [1, ..[2]] // t\n\n// c1\ncase list {\n    _ -> 0\n}\n",
		},
		{
			// It stays inside the body whose last line it could not end.
			"on the last line of a body",
			"interface Greeter {\n  fn greet( // c1\nvalue: self): String // t2\n}\n",
			"interface Greeter {\n    fn greet(value: self): String // t2\n    // c1\n}\n",
		},
		{
			// A dangling comment before a node that starts a line goes on
			// the line above it.
			"before an entry of a broken map pattern",
			"fn f(j: Maybe<Map<String, Int>>): Int {\n    case j {\n        Some({ // t10\n            \"a_rather_long_key_rather_long_key_rather_long_key\" => a,\n            \"b_rather_long_key_rather_long_key_rather_long_key\" => b,\n        }) -> a + b\n        _ -> 0\n    }\n}\n",
			"fn f(j: Maybe<Map<String, Int>>): Int {\n    case j {\n        Some({\n            // t10\n            \"a_rather_long_key_rather_long_key_rather_long_key\" => a,\n            \"b_rather_long_key_rather_long_key_rather_long_key\" => b,\n        }) ->\n            a + b\n\n        _ ->\n            0\n    }\n}\n",
		},
		{
			// A case arm's `->` takes no comment after it, so one that
			// would end the arm's line goes above the arm.
			"on a line that ends in a case arm's arrow",
			"fn area(s: Shape): Int {\n    case s {\n        Shape.Rect{ // c2\n            width_rather_long_rather_long_rather_long_rather_long_rather_long, height} -> width_rather_long_rather_long_rather_long_rather_long_rather_long * height\n        Shape.Point -> 0\n    }\n}\n",
			"fn area(s: Shape): Int {\n    case s {\n        // c2\n        Shape.Rect{width_rather_long_rather_long_rather_long_rather_long_rather_long, height} ->\n            width_rather_long_rather_long_rather_long_rather_long_rather_long * height\n\n        Shape.Point ->\n            0\n    }\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// A comment the parse keeps in a Trivia is no dangling comment: each is
// written once.
func TestFormat_ClaimedCommentsAreNotDangling(t *testing.T) {
	src := "// lead\nfn f() {\n    x = 1 // t\n    // end\n}\n\n// file end\n"
	formatsKeepingMeaning(t, src, src)
}
