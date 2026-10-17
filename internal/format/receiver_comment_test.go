package format

import "testing"

// A line ending in `self` does not end its statement, so `self // note`
// then `.get(b)` on the next line is one access whose receiver carries the
// comment. The comment stays at the end of the receiver's line, and the
// access continues on the line below.
func TestFormat_CommentAfterReceiverKeepsItsLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn read(b: Box) -> Int {\n  self // c1\n   .get(b)\n}\n",
			"fn read(b: Box) -> Int {\n    self // c1\n        .get(b)\n}\n",
		},
		{
			"fn read(b: Box) -> Int {\n    g(self // c3\n        .get(b))\n}\n",
			"fn read(b: Box) -> Int {\n    g(\n        self // c3\n            .get(b)\n    )\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
