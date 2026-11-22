package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/semtokens"
)

// The effects a call has.
//
// A hover on a call that has an effect, or on a read of a std `once` whose
// initializer has one (`io.stdout`), ends with one line saying the
// effect ("Has effects: `std::fs`.") and whether nomi.toml's lists allow
// it (effects.ReachedLine), and a hover on a function or `once` of the
// program, or a call or read of one, with a line for each effect it can have
// (effects.FunctionReach). A call or function that can act with no effect
// a list names says it acts on what it is passed (effects.HeldLine), and a
// call through an interface only some of whose implementations can act
// says it may (effects.MayLine). A function that reads or replaces app
// fields then has a line naming them (effects.UsesLine). The lines come
// from what a lowering of the program found (analyzedlowering.Reached).
// The language server passes the calls its last lowering runs found; the
// tour's editors pass those of a lowering of the block's program
// (vmhost.ReachedMarked). AppendReach is the one place that picks the
// calls a hover shows and appends their lines, so both show the same
// thing. MarkReaching marks the semantic token at each place the lowering
// marked (Reached.Marked): a call the compiler cannot show is pure, and
// the name of a function whose body holds one. Both editors draw it bold.
// A read of a `once` and a function named as a value keep their line and
// are not marked.

// AppendReach is content, a hover's markdown at pos, with the line of
// each call in reached that the hover is on appended after a blank line:
// a call in the hovered file (inFile, given a call's Path) whose callee
// holds pos, or a call with no callee in source (a `host fn`'s
// declaration, whose span is a point) that starts where tok, the token
// under pos, starts. Each line is given once, in reached's order. A line
// that fills (Reached.Fills) is given only when no other is: one program's
// lowering may find a function its roots do not reach that another
// program's does.
func AppendReach(content string, reached []analyzedlowering.Reached, inFile func(path string) bool, pos, tok analysis.Pos) string {
	before := func(l1, c1, l2, c2 int) bool { return l1 < l2 || l1 == l2 && c1 < c2 }
	at := func(r analyzedlowering.Reached) bool {
		return inFile(r.Path) && (!before(pos.Line, pos.Col, r.Line, r.Col) && before(pos.Line, pos.Col, r.EndLine, r.EndCol) ||
			r.EndLine == r.Line && r.EndCol == r.Col && tok.Line == r.Line && tok.Col == r.Col)
	}
	reaching := false
	for _, r := range reached {
		if !r.Fills && !r.Uses && at(r) {
			reaching = true
			break
		}
	}
	shown := map[string]bool{}
	for _, r := range reached {
		if !at(r) || shown[r.Text] || r.Fills && reaching {
			continue
		}
		shown[r.Text] = true
		content += "\n\n" + r.Text
	}
	return content
}

// MarkReaching is toks, the semantic tokens of the file inFile names, whose
// text is content, with byte columns (semtokens.Collect), with Effect set
// on each token that names the code at a place in reached that the
// lowering marked (Reached.Marked): the last token of a call's callee, the
// one that ends where its span ends (the `read_text` of `fs.read_text`, a
// function's name in its declaration), or, for a place that is a point,
// the token that starts there. A marked place that is a reserved word, the
// `dbg` of a `dbg`, which is the call that prints, has no token, since a
// reserved word names nothing and the grammars color it; it gets a keyword
// token of its own, marked. So a token is marked exactly when a hover on
// it ends with a line saying the call can act, and a qualifier before the
// name is left plain.
func MarkReaching(toks []semtokens.Token, reached []analyzedlowering.Reached, inFile func(path string) bool, content string) []semtokens.Token {
	if len(reached) == 0 {
		return toks
	}
	type at struct{ line, col int }
	ends := map[at]at{}
	points := map[at]bool{}
	// reserved reports whether the place r is one reserved word.
	var lines []string
	reserved := func(r analyzedlowering.Reached) bool {
		if r.EndLine != r.Line {
			return false
		}
		if lines == nil {
			lines = strings.Split(content, "\n")
		}
		if r.Line < 1 || r.Line > len(lines) || r.Col < 1 || r.EndCol-1 > len(lines[r.Line-1]) {
			return false
		}
		return lexer.IsReservedWord(lines[r.Line-1][r.Col-1 : r.EndCol-1])
	}
	var keywords []semtokens.Token
	keyword := map[at]bool{}
	for _, r := range reached {
		if !r.Marked || !inFile(r.Path) {
			continue
		}
		if r.EndLine == r.Line && r.EndCol == r.Col {
			points[at{r.Line, r.Col}] = true
			continue
		}
		ends[at{r.EndLine, r.EndCol}] = at{r.Line, r.Col}
		if !keyword[at{r.Line, r.Col}] && reserved(r) {
			keyword[at{r.Line, r.Col}] = true
			keywords = append(keywords, semtokens.Token{Line: r.Line, Col: r.Col, Length: r.EndCol - r.Col, Type: "keyword", Effect: true})
		}
	}
	for i, t := range toks {
		start, inSpan := ends[at{t.Line, t.Col + t.Length}]
		inSpan = inSpan && (t.Line > start.line || t.Line == start.line && t.Col >= start.col)
		if inSpan || points[at{t.Line, t.Col}] {
			toks[i].Effect = true
		}
	}
	return append(toks, keywords...)
}
