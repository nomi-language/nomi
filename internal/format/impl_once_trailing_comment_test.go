package format

import "testing"

// A comment after an impl block's `once` binding, on its line, stays with
// it, as one after a `fn` item does. It used to become the next item's
// leading comment, with blank lines around it that the next format changed.
func TestFormat_ImplOnceKeepsTrailingComment(t *testing.T) {
	src := "pub struct Limits {\n    low: Int\n}\n\nimpl Limits {\n    pub once top = 10 // t2\n\n    pub once next = Limits.top + 1 // t3\n}\n"
	formatsKeepingMeaning(t, src, src)
}
