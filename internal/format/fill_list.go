package format

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// filledList is bracedList for a literal whose elements are all number
// literals (fillTexts): flat when it fits, and otherwise broken after the
// opening bracket with as many elements on each line as fit the width,
// each followed by `,`, then the closing bracket on a line of its own.
//
// The packing is decided where the list renders (WithIndent), from the
// indent its elements start at, so it depends on the syntax tree and the
// list's position only. Flat, every separator renders `, `, whichever line
// the packing put it on, so the flat form is bracedList's.
//
// With broken (the source breaks the line after the opening bracket,
// brokenAfterOpen) the list is filled broken even when it would fit flat,
// as LocalBroken, as a kept-broken list of any other elements is. Its
// output has that break too, so it stays filled.
func filledList(open, close string, texts []string, broken bool) Doc {
	widths := make([]int, len(texts))
	for i, s := range texts {
		widths[i] = textWidth(s)
	}
	body := WithIndent(func(indent, width int) Doc {
		// Each line ends in a `,`: a separator's on every line but the
		// last, and the trailing comma on the last.
		lines := packGreedy(widths, 0, 0, width-indent, 1)
		parts := make([]Doc, 0, len(texts)*2)
		i := 0
		for l, line := range lines {
			if l > 0 {
				parts = append(parts, Text(","), Line())
			}
			for k := range line {
				if k > 0 {
					parts = append(parts, Text(", "))
				}
				parts = append(parts, Text(texts[i]))
				i++
			}
		}
		return Concat(parts...)
	})
	if broken {
		return LocalBroken(Concat(
			Text(open),
			Nest(defaultIndent, Concat(HardLine(), body)),
			Text(","),
			HardLine(),
			Text(close),
		))
	}
	return Group(Concat(
		Text(open),
		Nest(defaultIndent, Concat(LineOrEmpty(), body)),
		IfBroken(Nil(), Text(",")),
		LineOrEmpty(),
		Text(close),
	))
}

// packGreedy splits entries of the given widths into lines, each holding as
// many as fit: entries on a line are separated by `, `, the first line
// starts at column first and the others at column rest, and each line must
// leave lineEnd columns after its last entry within limit. A line holds at
// least one entry, so one wider than the limit takes a line of its own. It
// answers each line's widths.
func packGreedy(widths []int, first, rest, limit, lineEnd int) [][]int {
	var lines [][]int
	var line []int
	col := first
	for _, w := range widths {
		if len(line) > 0 && col+2+w+lineEnd > limit {
			lines = append(lines, line)
			line, col = nil, rest
		}
		if len(line) > 0 {
			col += 2
		}
		line = append(line, w)
		col += w
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

// fillTexts answers the text of each item when every one is a number
// literal a broken list packs several to a line (filledList): an Int,
// Float or Decimal literal, or one of those after a unary `-`. Anything
// else (a string, codepoint or boolean, a name, a dot variant, a call, a
// compound literal) and anything holding a comment answer false, and the
// list is written one item per line. Strings are left out because a list
// of them is usually read one per line: messages, rows of a grid.
func fillTexts(items []ast.Node) ([]string, bool) {
	if len(items) == 0 || nodesHaveComments(items) {
		return nil, false
	}
	texts := make([]string, 0, len(items))
	for _, it := range items {
		if !fillsNumber(it) {
			return nil, false
		}
		s, ok := plainText(emit(it))
		if !ok || strings.Contains(s, "\n") {
			return nil, false
		}
		texts = append(texts, s)
	}
	return texts, true
}

// fillsNumber reports whether n is a number literal fillTexts accepts.
// Parentheses around it do not count: the formatter drops them from a
// literal, so the next format would find the literal alone.
func fillsNumber(n ast.Node) bool {
	switch v := withoutParens(n).(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
		return true
	case *ast.Unary:
		if v.Op != "-" {
			return false
		}
		switch withoutParens(v.Right).(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
			return true
		}
	}
	return false
}

// withoutParens is n inside any parentheses around it.
func withoutParens(n ast.Node) ast.Node {
	for g, ok := n.(*ast.GroupedExpr); ok && g.Expr != nil; g, ok = n.(*ast.GroupedExpr) {
		n = g.Expr
	}
	return n
}

// plainText is the text d renders when it is only text, and false when it
// holds anything else (a line break, a comment).
func plainText(d Doc) (string, bool) {
	var sb strings.Builder
	var walk func(Doc) bool
	walk = func(d Doc) bool {
		switch v := d.(type) {
		case docNil:
			return true
		case docText:
			sb.WriteString(v.s)
			return true
		case docConcat:
			return walk(v.a) && walk(v.b)
		}
		return false
	}
	if !walk(d) {
		return "", false
	}
	return sb.String(), true
}
