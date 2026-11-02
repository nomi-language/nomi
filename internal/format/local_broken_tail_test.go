package format

import "testing"

// A struct literal broken for width keeps its fields on their own lines on
// the next format. A literal before it on the line measures the rest of its
// line up to the kept literal's `{` on both passes, so it stays flat on the
// second one too. It broke there when the text after the kept literal's
// `}` was charged to the line the kept literal opens on.
func TestFormat_KeptLiteralEndsTheLineBeforeIt(t *testing.T) {
	long := "a_name_long_enough_that_the_whole_sum_does_not_fit_on_one_line_even_below_the_equals"
	src := "fn main() {\n    x = A{a} + B{a: 0, b} + " + long + "\n}\n"
	want := "fn main() {\n    x = A{a} + B{\n        a: 0,\n        b,\n    } + " + long + "\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got != want {
		t.Fatalf("first format:\n%s\nwant:\n%s", got, want)
	}
	if err := SameMeaning(src, got); err != nil {
		t.Fatalf("the format changed the meaning: %v", err)
	}
	roundTrip(t, want)
}
