package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// isSpanTrivia reports whether a token lies outside any declaration's
// extent: separators, comments and blank lines.
func isSpanTrivia(t token.TokenType) bool {
	switch t {
	case token.NEWLINE, token.SEMICOLON, token.COMMENT, token.DOC_COMMENT, token.BLANK_LINE, token.EOF:
		return true
	}
	return false
}

// spanSince is the extent of the tokens consumed from tokens[start] to
// the cursor, with trivia at either end left out. It is zero when those
// tokens are all trivia.
func (p *Parser) spanSince(start int) ast.Span {
	end := p.pos
	if end > len(p.tokens) {
		end = len(p.tokens)
	}
	for start < end && isSpanTrivia(p.tokens[start].Type) {
		start++
	}
	for end > start && isSpanTrivia(p.tokens[end-1].Type) {
		end--
	}
	if start >= end {
		return ast.Span{}
	}
	first := p.tokens[start]
	endLine, endCol := tokenEndPos(p.tokens[end-1])
	return ast.Span{StartLine: first.Line, StartCol: first.Col, EndLine: endLine, EndCol: endCol}
}

// recordSpan sets node's extent to the tokens consumed since start, when
// node carries one.
func (p *Parser) recordSpan(node ast.Node, start int) {
	if n, ok := node.(ast.HasSpan); ok {
		if sp := p.spanSince(start); !sp.IsZero() {
			n.SetSpan(sp)
		}
	}
}

// tokenEndPos is one past a token's last byte. A token whose lexeme spans
// lines (a multi-line string) ends on its last line.
func tokenEndPos(t token.Token) (line, col int) {
	if t.EndCol > 0 {
		if t.EndLine > 0 {
			return t.EndLine, t.EndCol
		}
		return t.Line, t.EndCol
	}
	i := strings.LastIndexByte(t.Lexeme, '\n')
	if i < 0 {
		return t.Line, t.Col + len(t.Lexeme)
	}
	line = t.Line + strings.Count(t.Lexeme, "\n")
	if t.EndLine > line {
		line = t.EndLine
	}
	return line, len(t.Lexeme) - i
}
