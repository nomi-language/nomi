package lsp

import "github.com/nomi-language/nomi/internal/analysis"

// completionPositions maps positions of the latest text, which completion
// reparses, into the text the snapshot's analysis was built from. The two
// differ while an edit waits for analysis: completion answers from the
// older analysis rather than wait for the new one.
//
// The texts are compared as one changed region between a common prefix
// and a common suffix. A position before the line the change starts on is
// the same in both texts. A position after the change is shifted by the
// change's length. A position on the change's first line before the
// change, or inside it, has no counterpart: the older analysis may hold a
// different node there (`Dat` where the text now says `Date`), so a lookup
// keyed by that position is not made, and completion reads the name from
// the scopes instead, as it does for a statement the checker never typed.
type completionPositions struct {
	// identity is set when the texts are equal.
	identity bool
	newOffs  []int
	oldOffs  []int
	// lineStart is the offset of the start of the line the change starts
	// on; changeEnd is the offset in the latest text where the common
	// suffix begins; delta is len(latest) - len(analyzed).
	lineStart, changeEnd, delta int
	// editPos is where the change starts, in the analyzed text.
	editPos analysis.Pos
}

func newCompletionPositions(analyzed, latest string) completionPositions {
	if analyzed == latest {
		return completionPositions{identity: true}
	}
	prefix := 0
	for prefix < len(analyzed) && prefix < len(latest) && analyzed[prefix] == latest[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(analyzed)-prefix && suffix < len(latest)-prefix &&
		analyzed[len(analyzed)-1-suffix] == latest[len(latest)-1-suffix] {
		suffix++
	}
	m := completionPositions{
		newOffs:   lineOffsets(latest),
		oldOffs:   lineOffsets(analyzed),
		changeEnd: len(latest) - suffix,
		delta:     len(latest) - len(analyzed),
	}
	m.lineStart = m.newOffs[lineIndexOf(m.newOffs, prefix)]
	m.editPos = offsetPos(m.oldOffs, prefix)
	return m
}

// toAnalyzed maps a position of the latest text into the analyzed text,
// reporting false when it has no counterpart there.
func (m completionPositions) toAnalyzed(p analysis.Pos) (analysis.Pos, bool) {
	if m.identity {
		return p, true
	}
	off := posToOffset(m.newOffs, p.Line, p.Col)
	switch {
	case off < m.lineStart:
		return p, true
	case off >= m.changeEnd:
		return offsetPos(m.oldOffs, off-m.delta), true
	}
	return analysis.Pos{}, false
}

// scopePos is the analyzed-text position whose scope stands for p's: p's
// counterpart when it has one, else where the change starts, which the
// older analysis places in the same block the user is typing in.
func (m completionPositions) scopePos(p analysis.Pos) analysis.Pos {
	if q, ok := m.toAnalyzed(p); ok {
		return q
	}
	return m.editPos
}

// analyzedKey maps an expression key of the reparsed latest text to the
// analyzed text's.
func (m completionPositions) analyzedKey(k exprKey) (exprKey, bool) {
	q, ok := m.toAnalyzed(analysis.Pos{Line: k.line, Col: k.col})
	if !ok {
		return exprKey{}, false
	}
	return exprKey{q.Line, q.Col, k.kind}, true
}

// offsetPos converts a byte offset into a 1-based position.
func offsetPos(offs []int, off int) analysis.Pos {
	line := lineIndexOf(offs, off)
	return analysis.Pos{Line: line + 1, Col: off - offs[line] + 1}
}
