package hoverdoc

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
)

// What a call reaches outside the machine.
//
// A hover on a call that reaches outside the machine ends with one line
// saying what it reaches and whether nomi.toml's lists allow it
// (capability.ReachedLine), and a hover on a function of the program, or a
// call of one, with a line for each name the function can reach
// (capability.FunctionReach), from what a lowering of the program found
// (analyzedlowering.Reached). The language server passes the
// calls its last lowering runs found; the tour's editors pass those of a
// lowering of the block's program (vmhost.ReachedMarked). AppendReach is
// the one place that picks the calls a hover shows and appends their
// lines, so both show the same thing.

// AppendReach is content, a hover's markdown at pos, with the line of
// each call in reached that the hover is on appended after a blank line:
// a call in the hovered file (inFile, given a call's Path) whose callee
// holds pos, or a call with no callee in source (a `go` binding's
// declaration, whose span is a point) that starts where tok, the token
// under pos, starts. Each line is given once, in reached's order.
func AppendReach(content string, reached []analyzedlowering.Reached, inFile func(path string) bool, pos, tok analysis.Pos) string {
	before := func(l1, c1, l2, c2 int) bool { return l1 < l2 || l1 == l2 && c1 < c2 }
	shown := map[string]bool{}
	for _, r := range reached {
		at := !before(pos.Line, pos.Col, r.Line, r.Col) && before(pos.Line, pos.Col, r.EndLine, r.EndCol) ||
			r.EndLine == r.Line && r.EndCol == r.Col && tok.Line == r.Line && tok.Col == r.Col
		if !at || shown[r.Text] || !inFile(r.Path) {
			continue
		}
		shown[r.Text] = true
		content += "\n\n" + r.Text
	}
	return content
}
