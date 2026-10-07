package format

import "testing"

// A call's last-argument lambda whose one-expression body carries a comment
// keeps its braces and the comment. It used to be written as the bare
// expression, `Iter.loop(|n = 0| n + 1)`, and the comment was dropped.
func TestFormat_TailLambdaKeepsItsBodyComment(t *testing.T) {
	src := "fn main() {\n" +
		"    r = Iter.loop(|n = 0| {\n" +
		"        // count up\n" +
		"        n + 1\n" +
		"    })\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("got:\n%s\nwant:\n%s", got, src)
	}
}
