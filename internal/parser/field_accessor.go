package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// parseFieldAccessor parses the field accessor shorthand `.name`, a chain
// `.address.city`, or a tuple index `.0`. The current token is the leading
// DOT and the next one a field name or index. A lower-case name after the
// dot is a field; a capitalized one is a `.Variant`, parsed by the caller.
//
// The chain is read here rather than by the postfix loop, so `.address.city`
// is one accessor rather than a field read of the function `.address`.
func (p *Parser) parseFieldAccessor() (ast.Node, error) {
	dot := p.peek()
	acc := &ast.FieldAccessor{Line: dot.Line, Col: dot.Col}
	for {
		p.advance() // consume DOT
		name := p.peek()
		p.advance() // consume the field name or index
		acc.Path = append(acc.Path, &ast.Ident{Name: name.Lexeme, Line: name.Line, Col: name.Col})
		if p.atEnd() || p.peek().Type != token.DOT {
			return acc, nil
		}
		if next := p.peekAt(1).Type; next != token.IDENT && next != token.INT {
			return acc, nil
		}
	}
}
