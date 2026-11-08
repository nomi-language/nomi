package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// parseFieldAccessor parses the field accessor shorthand `.name`, a chain
// `.address.city`, or a tuple item `.first`. The current token is the
// leading DOT and the next one a field name. A lower-case name after the
// dot is a field; a capitalized one is a `.Variant`, parsed by the caller.
//
// The chain is read here rather than by the postfix loop, so `.address.city`
// is one accessor rather than a field read of the function `.address`. A
// number after the dot (`.0`, or `.1.0`, which the lexer reads as one
// Float) is the error that names the ordinals instead, and the parse goes on
// with them.
func (p *Parser) parseFieldAccessor() (ast.Node, error) {
	dot := p.peek()
	acc := &ast.FieldAccessor{Line: dot.Line, Col: dot.Col}
	for {
		p.advance() // consume DOT
		name := p.peek()
		p.advance() // consume the field name or index
		if name.Type == token.INT || name.Type == token.FLOAT {
			names, err := p.tupleIndexError("", name)
			if err != nil {
				return nil, err
			}
			col := name.Col
			for i, n := range names {
				acc.Path = append(acc.Path, &ast.Ident{Name: n, Line: name.Line, Col: col})
				col += len(strings.Split(name.Lexeme, ".")[i]) + 1
			}
		} else {
			acc.Path = append(acc.Path, &ast.Ident{Name: name.Lexeme, Line: name.Line, Col: name.Col})
		}
		if p.atEnd() || p.peek().Type != token.DOT {
			return acc, nil
		}
		if next := p.peekAt(1); next.Type != token.IDENT && !isTupleIndexToken(next) {
			return acc, nil
		}
	}
}
