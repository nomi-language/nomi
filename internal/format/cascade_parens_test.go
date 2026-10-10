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

// A literal the author stacked keeps the layout it was written in unless it
// is written as the depth cascade writes it (cascadeShaped). A parenthesized
// literal counted as cascade-shaped however it was written, so a list of
// `(Point{...})` on one line each read back as the cascade's and broke every
// literal on the next format.
func TestFormat_ParenthesizedLiteralIsNoCascadeShape(t *testing.T) {
	src := "fn main() {\n" +
		"    b = Bag{\n" +
		"        items_rather_long: [(Point{x_rather_long_rather_long_rather_long_rather_long: 1}), (Point{x_rather_long_rather_long_rather_long_rather_long: 2})],\n" +
		"        label: \"left\",\n" +
		"    }\n" +
		"}\n"
	want := "fn main() {\n" +
		"    b = Bag{\n" +
		"        items_rather_long: [\n" +
		"            (Point{x_rather_long_rather_long_rather_long_rather_long: 1}),\n" +
		"            (Point{x_rather_long_rather_long_rather_long_rather_long: 2}),\n" +
		"        ],\n" +
		"        label: \"left\",\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
