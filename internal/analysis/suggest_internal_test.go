package analysis

import "testing"

func TestClosestName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
	}{
		{"prnt", []string{"print", "println", "read_line"}, "print"},
		// An adjacent transposition is one edit.
		{"pritn", []string{"print", "println"}, "print"},
		// A difference in case alone is the closest there is.
		{"point", []string{"Point", "Paint"}, "Point"},
		// Too far: two edits in a four-letter name is past a third of it.
		{"prxx", []string{"print"}, ""},
		// Two equally close candidates: no guess.
		{"cat", []string{"bat", "hat"}, ""},
		// Two edits each; the one whose edits are partly case wins.
		{"Values", []string{"valuex", "Valu"}, "valuex"},
		// The name itself and synthesized names are never suggested.
		{"total", []string{"total", "__total", "$total"}, ""},
		{"", []string{"a"}, ""},
	} {
		if got := closestName(tc.name, tc.candidates); got != tc.want {
			t.Errorf("closestName(%q, %q) = %q, want %q", tc.name, tc.candidates, got, tc.want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "ab", 1},
		{"abc", "abd", 1},
		{"abcd", "abdc", 1},
		{"kitten", "sitting", 3},
	} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
