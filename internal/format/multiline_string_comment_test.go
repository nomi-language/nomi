package format

import "testing"

// A comment after a multi-line string's closing delimiter is the
// statement's trailing comment, on the line the string ends on. It used to
// be read as the next statement's leading comment, written on its own line
// with a blank line after it, which the next format dropped.
func TestFormat_CommentAfterMultilineStringTrails(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n    pattern = `\n        a\n        ` // why\n\n    assert pattern == \"a\"\n}\n",
		"fn main() {\n    text = \"\"\"\n        a\n        \"\"\" // why\n\n    assert text == \"a\"\n}\n",
	} {
		formatsKeepingMeaning(t, src, src)
	}
}
