package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/token"
)

// maxIndexHintText is the longest expression or index the subscript error
// quotes; a longer one is shown as `xs` or `i`.
const maxIndexHintText = 24

// subscriptAt reports the error for a `[` written right against the end of
// an operand, `xs[1]`, `f(x)[0]` or `m[k][j]`: Nomi has no subscript, and
// without this the `[` reads as a list literal that starts the next
// statement, or as a stray token in an argument list. start is the index of
// the operand's first token. A `[` after a space or a line break is a list
// literal of its own, and a type name's `[`, `Vector[1, 2]`, is a named
// literal that parsePrefix already took, so neither reaches here.
func (p *Parser) subscriptAt(start int) error {
	if p.atEnd() || p.pos == 0 {
		return nil
	}
	open := p.peek()
	if open.Type != token.LBRACKET {
		return nil
	}
	prev := p.tokens[p.pos-1]
	switch prev.Type {
	case token.IDENT, token.SELF, token.RPAREN, token.RBRACKET:
	default:
		return nil
	}
	if line, col := tokenEndPos(prev); line != open.Line || col != open.Col {
		return nil
	}
	expr, ok := shownTokens(p.tokens[start:p.pos])
	if !ok {
		expr = "xs"
	}
	index := "i"
	depth := 0
	for j := p.pos + 1; j < len(p.tokens); j++ {
		switch p.tokens[j].Type {
		case token.LBRACKET, token.LPAREN, token.LBRACE:
			depth++
			continue
		case token.RPAREN, token.RBRACE:
			depth--
			continue
		case token.RBRACKET:
			if depth > 0 {
				depth--
				continue
			}
			if text, ok := shownTokens(p.tokens[p.pos+1 : j]); ok {
				index = text
			}
		default:
			continue
		}
		break
	}
	return errorAt(open.Line, open.Col,
		"Nomi has no `%s[%s]`: read an element with `List.get(%s, %s)`, which returns an Option (`Vector.get`, `Map.get` and `String.at` for those types), or take it apart with a pattern",
		expr, index, expr, index)
}

// shownTokens is the text of toks as written, when they sit on one line,
// hold no string or comment, and are short enough to quote in an error.
func shownTokens(toks []token.Token) (string, bool) {
	if len(toks) == 0 {
		return "", false
	}
	var b strings.Builder
	for i, t := range toks {
		if t.Lexeme == "" || strings.HasPrefix(tokenShown(t), "a ") || t.Line != toks[0].Line ||
			t.Type == token.ILLEGAL || t.Type == token.CODEPOINT_LITERAL {
			return "", false
		}
		if i > 0 {
			if _, col := tokenEndPos(toks[i-1]); col != t.Col {
				b.WriteByte(' ')
			}
		}
		b.WriteString(t.Lexeme)
		if b.Len() > maxIndexHintText {
			return "", false
		}
	}
	return b.String(), true
}
