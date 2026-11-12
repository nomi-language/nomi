package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/token"
)

// tokensText is the source of tokens[from:to], which sit on one line, as
// written. When a token's text cannot be recovered from its lexeme, it is
// fallback.
func (p *Parser) tokensText(from, to int, fallback string) string {
	var b strings.Builder
	endCol := 0
	for i := from; i < to && i < len(p.tokens); i++ {
		t := p.tokens[i]
		if t.Line != p.tokens[from].Line || t.EndLine > t.Line {
			return fallback
		}
		text := t.Lexeme
		if t.EndCol > 0 && t.EndCol-t.Col != len(text) {
			switch {
			case t.Type == token.STRING_LITERAL && t.EndCol-t.Col == len(text)+2:
				text = `"` + text + `"`
			default:
				return fallback
			}
		}
		if endCol > 0 {
			b.WriteString(strings.Repeat(" ", max(t.Col-endCol, 0)))
		}
		b.WriteString(text)
		endCol = t.Col + len(text)
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

// strayBodyMarker checks the cursor, at the start of a statement in a body
// or of a case arm, for a `///` doc comment, or a `//!` attached test that
// no declaration follows. Neither can go there: a doc comment documents the
// declaration after it, and an attached test tests one. The likely intent
// is a comment, such as a line commented out, so strayBodyMarker records
// the error with that fix, steps over the run of such lines as if they
// were comments, and reports true.
func (p *Parser) strayBodyMarker() bool {
	if p.atEnd() {
		return false
	}
	tok := p.peek()
	if tok.Type != token.DOC_COMMENT && tok.Type != token.TEST_PROMPT {
		return false
	}
	marks, after := p.markerRun(tok.Type)
	var message string
	if tok.Type == token.DOC_COMMENT {
		message = "a `///` doc comment documents the declaration after it, so it can't go inside a body; use `//` for a comment here"
	} else {
		if p.startsDeclarationAt(after) {
			return false
		}
		message = "a `//!` attached test goes above the declaration it tests, so it can't go before a statement in a body; use `//` for a comment here"
	}
	p.softError(ParseError{Line: tok.Line, Col: tok.Col, Message: message, Fix: &Fix{Markers: marks}})
	p.pos = after
	return true
}

// markerRun reads the run of lines starting with a tt marker (`///` or
// `//!`) at the cursor, without moving it: each marker's position, and the
// index of the first token after the run. A blank line ends the run.
func (p *Parser) markerRun(tt token.TokenType) ([]Pos, int) {
	var marks []Pos
	last := 0 // the last line the run covers
	i := p.pos
	for ; i < len(p.tokens); i++ {
		t := p.tokens[i]
		switch {
		case t.Type == tt && (last == 0 || t.Line == last+1):
			marks = append(marks, Pos{t.Line, t.Col})
			last = tokenEndLine(t)
		case t.Type == token.NEWLINE || t.Type == token.SEMICOLON:
		case t.Line <= last && t.Type != token.EOF:
			// A `//!` line's code.
			last = max(last, tokenEndLine(t))
		default:
			return marks, i
		}
	}
	return marks, i
}

// startsDeclarationAt reports whether the first token at or after i that is
// not layout or a comment begins a declaration an attached test can sit
// above.
func (p *Parser) startsDeclarationAt(i int) bool {
	for i < len(p.tokens) {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT:
			i++
			continue
		}
		break
	}
	if i >= len(p.tokens) {
		return false
	}
	t := p.tokens[i]
	switch t.Type {
	case token.TEST_PROMPT, token.DOC_COMMENT, token.AT, token.PUB, token.EXTERN,
		token.FN, token.ONCE, token.TYPE, token.STRUCT, token.ENUM, token.TYPEALIAS,
		token.INTERFACE, token.IMPL, token.OPAQUE, token.TEST:
		return true
	}
	if i+1 < len(p.tokens) && isGroupStart(t, p.tokens[i+1]) {
		return true
	}
	if isHostToken(t) && i+1 < len(p.tokens) {
		next := p.tokens[i+1].Type
		return next == token.FN || next == token.TYPE
	}
	return false
}
