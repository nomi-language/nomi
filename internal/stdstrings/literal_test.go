package stdstrings

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// String.to_literal's text lexes as one string literal that reads back as
// the string, whatever it holds.
func TestLiteralReadsBackAsTheString(t *testing.T) {
	for _, s := range []string{"", "plain", "a\tb\nc\rd", `q"b\s`, "$5 ${x} \\${y}", "\x00\x1f\x7f", "é 🙂 ​", "\r\n"} {
		lit := Literal(s)
		toks := lexer.Lex(lit)
		if len(toks) < 1 || toks[0].Type != token.STRING_LITERAL {
			t.Errorf("Literal(%q) = %s lexes as %v", s, lit, toks)
			continue
		}
		if toks[0].Lexeme != s {
			t.Errorf("Literal(%q) = %s reads back as %q", s, lit, toks[0].Lexeme)
		}
	}
}
