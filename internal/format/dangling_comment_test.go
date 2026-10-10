package format

import "testing"

// A comment where the syntax tree has no trivia slot is a dangling comment
// on the node nearest it (parser.attachDangling), and the formatter writes
// it at the end of the line that node starts or ends on, or on a line of its
// own above it.
func TestFormat_DanglingComments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"after a list spread's ..",
			"xs = [1, .. // rest\n[2]]\n",
			"xs = [1, ..[2]] // rest\n",
		},
		{
			"after a with statement's =",
			"fn publish_quietly() {\n    with App.logger = // why\n        Silent\n    publish()\n}\n",
			"fn publish_quietly() {\n    with App.logger = Silent // why\n    publish()\n}\n",
		},
		{
			// The line already ends in a comment, so the dangling one takes
			// a line of its own, above the next line with code.
			"on a line that has a comment",
			"list = [1, .. // c1\n          [2]] // t\n\ncase list {\n    _ -> 0\n}\n",
			"list = [1, ..[2]] // t\n\n// c1\ncase list {\n    _ -> 0\n}\n",
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
