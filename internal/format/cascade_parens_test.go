package format

import "testing"

// Parentheses add no level of literal nesting. compoundDepth stopped at
// them, so `[(first_or([[8.5]], [2.5]))]` was too shallow to cascade, the
// formatter dropped the parentheses around the call, and the next format
// found it deep enough and cascaded.
func TestFormat_DepthCascadeSeesThroughParens(t *testing.T) {
	src := "fn main() {\n    xs = Iter.map([(first_or([[8.5], [1.5]], [2.5]))], cb7)\n}\n"
	got := formatTwice(t, src)
	if err := SameMeaning(src, got); err != nil {
		t.Fatal(err)
	}
	flat, err := Format("fn main() {\n    xs = Iter.map([first_or([[8.5], [1.5]], [2.5])], cb7)\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != flat {
		t.Errorf("with parentheses:\n%s\nwithout:\n%s", got, flat)
	}
}
