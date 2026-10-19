package format

import "testing"

// Parentheses add no level of literal nesting. compoundDepth stopped at
// them, so `[(first_or([[8.5]], [2.5]))]` was too shallow to break for its
// depth, the formatter dropped the parentheses around the call, and the
// next format found it deep enough and broke it.
func TestFormat_DepthBreakSeesThroughParens(t *testing.T) {
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

// A list of parenthesized struct literals too wide for its line breaks one
// element per line, and each element stays on its line.
func TestFormat_ParenthesizedLiteralsBreakByWidth(t *testing.T) {
	src := "fn main() {\n" +
		"    b = #Bag{\n" +
		"        items_rather_long: [(#Point{x_rather_long_rather_long_rather_long_rather_long: 1}), (#Point{x_rather_long_rather_long_rather_long_rather_long: 2})],\n" +
		"        label: \"left\",\n" +
		"    }\n" +
		"}\n"
	want := "fn main() {\n" +
		"    b = #Bag{\n" +
		"        items_rather_long: [\n" +
		"            (#Point{x_rather_long_rather_long_rather_long_rather_long: 1}),\n" +
		"            (#Point{x_rather_long_rather_long_rather_long_rather_long: 2}),\n" +
		"        ],\n" +
		"        label: \"left\",\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
