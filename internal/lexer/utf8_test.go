package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// A string literal holding a byte that is not UTF-8 is an ILLEGAL token, in
// every string form. Valid multi-byte text is not.
func TestStringLiteralMustBeUTF8(t *testing.T) {
	for _, src := range []string{
		"\"\x81\"",
		"\"a${x}\xff\"",
		"\"\"\"\n    \xc3\n    \"\"\"",
		"`\x81`",
		"`\n    \xe2\x82\n    `",
		"test \"\x81\" {}",
	} {
		found := false
		for _, tok := range Lex(src) {
			if tok.Type == token.ILLEGAL && tok.Problem == invalidUTF8InString {
				found = true
			}
		}
		if !found {
			t.Errorf("Lex(%q) = %+v, want an ILLEGAL token for the byte that is not UTF-8", src, Lex(src))
		}
	}
	for _, src := range []string{"\"é€😀\"", "`é`", "\"\"\"\n    é\n    \"\"\"", "x = 1 // \x81"} {
		for _, tok := range Lex(src) {
			if tok.Type == token.ILLEGAL {
				t.Errorf("Lex(%q) has an ILLEGAL token %+v", src, tok)
			}
		}
	}
}
