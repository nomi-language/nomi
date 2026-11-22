package hoverdoc

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/semtokens"
)

// MarkReaching marks the last token of each place the lowering marked, and
// no token of a place it gave a line and no mark, such as a read of a
// `once` or an app fields line.
func TestMarkReaching_MarksOnlyMarkedPlaces(t *testing.T) {
	inFile := func(string) bool { return true }
	toks := []semtokens.Token{
		{Line: 2, Col: 5, Length: 6, Type: "namespace"},  // Writer
		{Line: 2, Col: 12, Length: 5, Type: "function"},  // print
		{Line: 2, Col: 18, Length: 2, Type: "namespace"}, // io
		{Line: 2, Col: 21, Length: 6, Type: "variable"},  // stdout
		{Line: 4, Col: 4, Length: 4, Type: "function"},   // main
	}
	toks = MarkReaching(toks, []analyzedlowering.Reached{
		{Path: "main.nomi", Line: 2, Col: 5, EndLine: 2, EndCol: 17, Text: "Has effects on what it is passed.", Marked: true},
		{Path: "main.nomi", Line: 2, Col: 20, EndLine: 2, EndCol: 27, Text: "Has effects: `std::io`."},
		{Path: "main.nomi", Line: 4, Col: 4, EndLine: 4, EndCol: 8, Text: "Uses `App.out`.", Uses: true},
	}, inFile, "")
	var marked []int
	for i, tok := range toks {
		if tok.Effect {
			marked = append(marked, i)
		}
	}
	if len(marked) != 1 || marked[0] != 1 {
		t.Errorf("the marked tokens are %v; want only the callee's last token, 1", marked)
	}
}

// A line that fills, found by a walk from what a program's roots do not
// reach, is shown only where no line of a walk that reaches the code is:
// a file two programs hold reads as the one that reaches it says.
func TestAppendReach_AFillingLineYieldsToAReachingOne(t *testing.T) {
	inFile := func(string) bool { return true }
	at := analysis.Pos{Line: 3, Col: 5}
	fill := analyzedlowering.Reached{Path: "rules.nomi", Line: 3, Col: 5, EndLine: 3, EndCol: 9, Text: "Has effects: `std::fs`.", Fills: true}
	reach := analyzedlowering.Reached{Path: "rules.nomi", Line: 3, Col: 5, EndLine: 3, EndCol: 9,
		Text: "Has effects: `std::fs`, which `nomi.toml` allows in `[effects.deps]`."}
	uses := analyzedlowering.Reached{Path: "rules.nomi", Line: 3, Col: 5, EndLine: 3, EndCol: 9, Text: "Uses `App.limit`.", Uses: true}
	for _, tc := range []struct {
		name    string
		reached []analyzedlowering.Reached
		want    string
	}{
		{"a filling line alone", []analyzedlowering.Reached{fill}, "\n\n" + fill.Text},
		{"a filling line before a reaching one", []analyzedlowering.Reached{fill, reach}, "\n\n" + reach.Text},
		{"a reaching line before a filling one", []analyzedlowering.Reached{reach, fill}, "\n\n" + reach.Text},
		{"a filling line beside an app fields line", []analyzedlowering.Reached{fill, uses}, "\n\n" + fill.Text + "\n\n" + uses.Text},
	} {
		if got := AppendReach("", tc.reached, inFile, at, at); got != tc.want {
			t.Errorf("%s: the hover ends with %q; want %q", tc.name, got, tc.want)
		}
	}
}
