package parser

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// keywordAsName is the error at tok, a reserved word written where a name
// stands, which role names ("a parameter", "a field"), or nil when tok is
// no reserved word. The error says what the word is for and offers a name
// to write instead (lexer.KeywordPurpose).
func keywordAsName(tok token.Token, role string) error {
	if tok.Type == token.IDENT || tok.Type == token.TYPE_IDENT {
		return nil
	}
	purpose, instead, ok := lexer.KeywordPurpose(tok.Lexeme)
	if !ok {
		return nil
	}
	return errorAt(tok.Line, tok.Col, "`%s` is a keyword (%s), so it can't name %s; name it something else, such as `%s`",
		tok.Lexeme, purpose, role, instead)
}
