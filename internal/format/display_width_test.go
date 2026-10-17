package format

import (
	"strings"
	"testing"
)

// A line's width is counted in the columns an editor shows: one per
// grapheme cluster, two for a wide one. A tuple of two 25-letter strings
// of `é` is 68 columns (118 bytes) and stays on one line; the same tuple
// of CJK letters is 118 columns and breaks.
func TestFormat_LineWidthCountsDisplayColumns(t *testing.T) {
	e := strings.Repeat("é", 25)
	combining := strings.Repeat("e\u0301", 25)
	wide := strings.Repeat("子", 25)
	for _, c := range []struct {
		name, text string
		breaks     bool
	}{
		{"precomposed", e, false},
		{"combining marks", combining, false},
		{"wide letters", wide, true},
	} {
		line := "    pair = #(\"" + c.text + "\", \"" + c.text + "\")"
		src := "fn main() {\n" + line + "\n    dbg pair\n}\n"
		got, err := Format(src)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if broke := got != src; broke != c.breaks {
			t.Errorf("%s: breaks = %v, want %v; got:\n%s", c.name, broke, c.breaks, got)
		}
	}
}

func TestTextWidth(t *testing.T) {
	for s, want := range map[string]int{
		"":                           0,
		"abc":                        3,
		"é":                          1,
		"e\u0301":                    1,
		"子猫":                         4,
		"ผู้เขียน":                   5,
		"a⬂€":                        3,
		"\U0001F469\u200D\U0001F4BB": 2,
	} {
		if got := textWidth(s); got != want {
			t.Errorf("textWidth(%q) = %d, want %d", s, got, want)
		}
	}
}
