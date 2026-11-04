package parser

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A named struct is built and matched by its name, with no symbol in
// front: `Point{x: 1}`, `geo.Point{x: 1}`, `Shape.Circle{radius: 1.0}`,
// `.Circle{radius: 1.0}`, and the patterns `Point{x, y}`, `.Circle{radius}`.
// A name with a `{` on its line opens the literal, except at the top of a
// condition head (the expression between `if` or `case` and the `{` of its
// body), where `Name {` is the name followed by the body. A literal there
// is written in parentheses: `case (Point{x: 1}) { ... }`.

// namedBraceAhead reports whether the tokens at offset start a literal
// name followed by a `{` on the name's line: `Point{`, `geo.Point{`,
// `Shape.Circle{`, `.Circle{`. n is the number of tokens in the name.
func (p *Parser) namedBraceAhead(offset int) (n int, ok bool) {
	i := offset
	switch t := p.peekAt(i); t.Type {
	case token.DOT:
		next := p.peekAt(i + 1)
		if next.Type != token.TYPE_IDENT || !adjacent(t, next) {
			return 0, false
		}
		i += 2
	case token.TYPE_IDENT:
		i++
		for p.peekAt(i).Type == token.DOT && p.peekAt(i+1).Type == token.TYPE_IDENT {
			i += 2
		}
	case token.IDENT, token.PATH:
		if p.peekAt(i+1).Type != token.DOT || p.peekAt(i+2).Type != token.TYPE_IDENT {
			return 0, false
		}
		i++
		for p.peekAt(i).Type == token.DOT && p.peekAt(i+1).Type == token.TYPE_IDENT {
			i += 2
		}
	default:
		return 0, false
	}
	open, last := p.peekAt(i), p.peekAt(i-1)
	if open.Type != token.LBRACE || open.Line != last.Line {
		return 0, false
	}
	return i - offset, true
}

// matchingBrace is the offset of the `}` that closes the `{` at offset, or
// -1 when the file ends first.
func (p *Parser) matchingBrace(offset int) int {
	depth := 0
	for i := offset; ; i++ {
		switch p.peekAt(i).Type {
		case token.EOF:
			return -1
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
}

// enterHead marks the start of a condition head, ended by the returned
// function.
func (p *Parser) enterHead() func() {
	p.heads = append(p.heads, p.pos)
	return func() { p.heads = p.heads[:len(p.heads)-1] }
}

// atHeadTop reports whether the parser stands at the top of the innermost
// condition head: no bracket opened since the head began is still open.
func (p *Parser) atHeadTop() bool {
	if len(p.heads) == 0 {
		return false
	}
	depth := 0
	for i := p.heads[len(p.heads)-1]; i < p.pos; i++ {
		switch p.tokens[i].Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth--
		}
	}
	return depth <= 0
}

// headLiteral is the error for a named literal or pattern written at the
// top of a condition head, `case (Point{x: 1}) {`: there `Name {` is the name
// and the body, so the literal goes in parentheses. It answers only when
// the source reads as a literal followed by the body, a `{` right after the
// name and another `{` after the literal's `}` on the same line; nil
// otherwise, and the name is read alone.
func (p *Parser) headLiteral(n int, first token.Token, what string) error {
	open := p.peekAt(n)
	last := p.peekAt(n - 1)
	if !adjacent(last, open) {
		return nil
	}
	end := p.matchingBrace(n)
	if end < 0 {
		return nil
	}
	closer, body := p.peekAt(end), p.peekAt(end+1)
	if body.Type != token.LBRACE || body.Line != closer.Line {
		return nil
	}
	return ParseError{Line: first.Line, Col: first.Col,
		Message: fmt.Sprintf("a %s in an `if` or `case` head is written in parentheses: the `{` after a name there opens the body", what),
		Fix: &Fix{Title: "Wrap it in parentheses", Edits: []Edit{
			{Start: Pos{first.Line, first.Col}, End: Pos{first.Line, first.Col}, Text: "("},
			{Start: Pos{closer.Line, closer.Col + 1}, End: Pos{closer.Line, closer.Col + 1}, Text: ")"},
		}}}
}

// parseLiteralName reads the name a named literal or pattern starts with,
// the cursor on its first token: `Point`, `geo.Point`, `Shape.Circle`,
// `.Circle`. Every segment but the last names the owner.
func (p *Parser) parseLiteralName() ast.TypeExpr {
	first := p.peek()
	if first.Type == token.DOT {
		p.advance() // consume DOT
		name := p.peek()
		p.advance() // consume TYPE_IDENT
		return &ast.DotVariantType{Name: name.Lexeme, Line: first.Line, Col: first.Col}
	}
	return p.literalNameSegments(first)
}

// tryNamedStructLit parses a named struct literal when one starts at the
// cursor. ok is false, with nothing consumed, when none does.
func (p *Parser) tryNamedStructLit() (ast.Node, bool, error) {
	n, ok := p.namedBraceAhead(0)
	if !ok {
		return nil, false, nil
	}
	if p.atHeadTop() {
		if err := p.headLiteral(n, p.peek(), "struct literal"); err != nil {
			return nil, true, err
		}
		return nil, false, nil
	}
	first := p.peek()
	typeName := p.parseLiteralName()
	lit, err := p.parseStructLit(typeName, first.Line, first.Col)
	return lit, true, err
}
