package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A named struct or map is built and matched by its name: `Point{x: 1}`,
// `geo.Point{x: 1}`, `Shape.Circle{radius: 1.0}`, `.Circle{radius: 1.0}`,
// `Kvs{"a" => 1}`, `.Obj{"k" => v}`, and the patterns `Point{x:, y:}`,
// `.Circle{radius:}`, `Kvs{"a" => v}`. A name with a `{` on its line opens
// the literal, wherever an expression or a pattern stands; its first entry,
// `name:` or `key =>`, says whether it is a struct or a map, and `Name{}`
// is the one the name declares (the checker decides).

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

// braceIsEmpty reports whether the `{` at offset holds nothing but line
// breaks, comments and `;` before its `}`. An empty named literal is a struct
// literal whatever trivia it holds, so `A{}` and `A{ // note }` parse alike
// and formatting one into the other keeps its meaning.
func (p *Parser) braceIsEmpty(offset int) bool {
	for i := p.pos + offset + 1; i < len(p.tokens); i++ {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT, token.SEMICOLON:
			continue
		case token.RBRACE:
			return true
		}
		return false
	}
	return false
}

// tryNamedStructLit parses a named struct literal when one starts at the
// cursor. ok is false, with nothing consumed, when none does.
func (p *Parser) tryNamedStructLit() (ast.Node, bool, error) {
	if _, ok := p.namedBraceAhead(0); !ok {
		return nil, false, nil
	}
	n, _ := p.namedBraceAhead(0)
	first := p.peek()
	isMap := p.braceOpensMap(n) && !p.braceIsEmpty(n)
	typeName := p.parseLiteralName()
	if isMap {
		lit, err := p.parseTypePrefixedMapLit(typeName, first.Line, first.Col)
		return lit, true, err
	}
	lit, err := p.parseStructLit(typeName, first.Line, first.Col)
	return lit, true, err
}
