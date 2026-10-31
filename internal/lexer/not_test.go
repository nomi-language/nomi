package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// `not` is a keyword, a word on its own: `nothing` and `not_null` are names.
// A `!` stays its own token, for the postfix unwrap and for the parser's
// error that names `not`.
func TestNotKeyword(t *testing.T) {
	assertTokens(t, "not x and not_null or nothing", []token.Token{
		{Type: token.NOT, Lexeme: "not"},
		{Type: token.IDENT, Lexeme: "x"},
		{Type: token.AND, Lexeme: "and"},
		{Type: token.IDENT, Lexeme: "not_null"},
		{Type: token.OR, Lexeme: "or"},
		{Type: token.IDENT, Lexeme: "nothing"},
		{Type: token.EOF},
	})
	// `is` is a keyword too, and `is_empty` a name.
	assertTokens(t, "x is Some(_) and is_empty", []token.Token{
		{Type: token.IDENT, Lexeme: "x"},
		{Type: token.IS, Lexeme: "is"},
		{Type: token.TYPE_IDENT, Lexeme: "Some"},
		{Type: token.LPAREN, Lexeme: "("},
		{Type: token.UNDERSCORE, Lexeme: "_"},
		{Type: token.RPAREN, Lexeme: ")"},
		{Type: token.AND, Lexeme: "and"},
		{Type: token.IDENT, Lexeme: "is_empty"},
		{Type: token.EOF},
	})
	assertTokens(t, "!x", []token.Token{
		{Type: token.BANG, Lexeme: "!"},
		{Type: token.IDENT, Lexeme: "x"},
		{Type: token.EOF},
	})
}
