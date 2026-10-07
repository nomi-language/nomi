package format

import "testing"

// A comment after an impl function's closing `}`, on its line, stays there.
// It used to become a comment on a line of its own above the next function,
// with a blank line after it that the next format removed, so formatting a
// formatted file changed it again.
func TestFormat_ImplFunctionKeepsItsTrailingComment(t *testing.T) {
	src := "impl R {\n" +
		"    fn a(): Int {\n" +
		"        1\n" +
		"    } // the first\n" +
		"\n" +
		"    fn b(): Int {\n" +
		"        2\n" +
		"    }\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("got:\n%s\nwant:\n%s", got, src)
	}
}
