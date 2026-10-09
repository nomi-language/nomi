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
// is one accessor rather than a field read of the function `.address`. An
// index pair the lexer reads as one Float (`.1.0`) is two path steps, as it
// is in `t.1.0`.
func (p *Parser) parseFieldAccessor() (ast.Node, error) {
	dot := p.peek()
	acc := &ast.FieldAccessor{Line: dot.Line, Col: dot.Col}
	for {
		p.advance() // consume DOT
		name := p.peek()
		p.advance() // consume the field name or index
		if first, second, ok := tupleIndexPair(name); ok {
			if err := checkTupleIndex(name, first, second); err != nil {
				return nil, err
			}
			acc.Path = append(acc.Path,
				&ast.Ident{Name: first, Line: name.Line, Col: name.Col},
				&ast.Ident{Name: second, Line: name.Line, Col: name.Col + len(first) + 1})
		} else {
			if name.Type == token.INT {
				if err := checkTupleIndex(name, name.Lexeme); err != nil {
					return nil, err
				}
			}
			acc.Path = append(acc.Path, &ast.Ident{Name: name.Lexeme, Line: name.Line, Col: name.Col})
		}
		if p.atEnd() || p.peek().Type != token.DOT {
			return acc, nil
		}
		if next := p.peekAt(1); next.Type != token.IDENT && next.Type != token.INT && !isTupleIndexPair(next) {
			return acc, nil
		}
	}
}
