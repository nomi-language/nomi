package format

import "testing"

// A spread of a range is written in parentheses, so the spread's `..` and
// the range's do not run together: `[0, ..1..4]` becomes `[0, ..(1..4)]`, in
// a list, a vector and a set alike, and a parenthesized one stays as it is.
func TestFormat_RangeSpreadIsParenthesized(t *testing.T) {
	src := "fn main() {\n" +
		"    a = [0, ..1..4, ..xs]\n" +
		"    b = #[..1..=3]\n" +
		"    c = %[..0..2, 5]\n" +
		"    d = [..(1..4)]\n" +
		"}\n"
	want := "fn main() {\n" +
		"    a = [0, ..(1..4), ..xs]\n" +
		"    b = #[..(1..=3)]\n" +
		"    c = %[..(0..2), 5]\n" +
		"    d = [..(1..4)]\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
