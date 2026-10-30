package parser

import (
	"errors"
	"math/big"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// intLiteral is the IntLit an INT token writes. An Int is exact at any
// size, so a literal of any length is one: a value that fits int64 is Value,
// and one that does not is Big. Only a malformed literal is an error.
func intLiteral(tok token.Token) (*ast.IntLit, error) {
	lit := &ast.IntLit{Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}
	val, err := strconv.ParseInt(tok.Lexeme, 0, 64)
	if err == nil {
		lit.Value = val
		return lit, nil
	}
	if !errors.Is(err, strconv.ErrRange) {
		return nil, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
	}
	z, ok := new(big.Int).SetString(tok.Lexeme, 0)
	if !ok {
		return nil, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
	}
	lit.Big = z
	return lit, nil
}
