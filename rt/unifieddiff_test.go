package rt

import (
	"fmt"
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	// thirty is the lines "1" to "30", each ending in a newline, with the
	// lines numbered in changed replaced by their word.
	thirty := func(changed map[int]string) string {
		var b strings.Builder
		for i := 1; i <= 30; i++ {
			if w, ok := changed[i]; ok {
				b.WriteString(w + "\n")
			} else {
				fmt.Fprintf(&b, "%d\n", i)
			}
		}
		return b.String()
	}
	cases := []struct {
		name, before, after, want string
	}{
		{"unchanged", "a\nb\n", "a\nb\n", ""},
		{"empty", "", "", ""},
		{"one changed line", "one\ntwo\nthree\n", "one\n2\nthree\n",
			"--- f\n+++ f\n@@ -1,3 +1,3 @@\n one\n-two\n+2\n three\n"},
		{"changes far apart", thirty(nil), thirty(map[int]string{5: "five", 25: "twenty-five"}),
			"--- f\n+++ f\n" +
				"@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n" +
				"@@ -22,7 +22,7 @@\n 22\n 23\n 24\n-25\n+twenty-five\n 26\n 27\n 28\n"},
		{"changes whose context meets", thirty(nil), thirty(map[int]string{5: "five", 12: "twelve"}),
			"--- f\n+++ f\n" +
				"@@ -2,14 +2,14 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n 9\n 10\n 11\n-12\n+twelve\n 13\n 14\n 15\n"},
		{"a missing final newline added", "a\nb", "a\nb\n",
			"--- f\n+++ f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"},
		{"an unchanged last line with no newline", "a\nb\nc", "x\nb\nc",
			"--- f\n+++ f\n@@ -1,3 +1,3 @@\n-a\n+x\n b\n c\n\\ No newline at end of file\n"},
		{"CRLF input", "a\r\nb\r\n", "a\nb\n",
			"--- f\n+++ f\n@@ -1,2 +1,2 @@\n-a\r\n-b\r\n+a\n+b\n"},
		{"from an empty file", "", "x\n",
			"--- f\n+++ f\n@@ -0,0 +1 @@\n+x\n"},
		{"to an empty file", "x\ny\n", "",
			"--- f\n+++ f\n@@ -1,2 +0,0 @@\n-x\n-y\n"},
		{"a line added in the middle", "a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nc\nd\nnew\ne\nf\ng\nh\n",
			"--- f\n+++ f\n@@ -2,6 +2,7 @@\n b\n c\n d\n+new\n e\n f\n g\n"},
	}
	for _, c := range cases {
		if got := UnifiedDiff("f", c.before, c.after); got != c.want {
			t.Errorf("%s: got:\n%q\nwant:\n%q", c.name, got, c.want)
		}
	}
}

// Past the comparison bound the diff still turns one side into the other:
// it replaces every line between the common prefix and suffix.
func TestUnifiedDiffPastTheBoundReplacesTheChangedLines(t *testing.T) {
	var before, after strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&before, "line %d\n", i)
		if i == 0 || i == 1999 {
			fmt.Fprintf(&after, "other %d\n", i)
		} else {
			fmt.Fprintf(&after, "line %d\n", i)
		}
	}
	got := UnifiedDiff("f", before.String(), after.String())
	if !strings.HasPrefix(got, "--- f\n+++ f\n@@ -1,2000 +1,2000 @@\n-line 0\n") {
		t.Fatalf("the diff does not open with one hunk of every line:\n%.200s", got)
	}
	if n := strings.Count(got, "\n-"); n != 2000 {
		t.Errorf("%d lines removed, want 2000", n)
	}
	if n := strings.Count(got, "\n+"); n != 2000+1 { // the +++ line
		t.Errorf("%d lines added, want 2000", n-1)
	}
}
