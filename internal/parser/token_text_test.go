package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// Every token an error can name has a name: none renders as nothing, and
// a malformed one is never shown by the lexer's name for it.
func TestTokenShownNamesEveryToken(t *testing.T) {
	for tt := token.TokenType(0); tt.String() != "UNKNOWN"; tt++ {
		got := tokenShown(token.Token{Type: tt})
		if strings.Trim(got, "` ") == "" {
			t.Errorf("tokenShown(%s with no text) = %q", tt, got)
		}
	}
	for _, src := range []string{"`abc", "`", "\"\"\"abc", "'a"} {
		toks := lexer.Lex(src)
		var illegal token.Token
		for _, tok := range toks {
			if tok.Type == token.ILLEGAL {
				illegal = tok
			}
		}
		if illegal.Type != token.ILLEGAL {
			t.Fatalf("%q lexes to no malformed token", src)
		}
		if got := tokenShown(illegal); !strings.HasPrefix(got, "an unterminated ") {
			t.Errorf("tokenShown(%q's malformed token) = %q, want it named as unterminated", src, got)
		}
	}
	if got := tokenShown(token.Token{Type: token.ILLEGAL}); got == "illegal" {
		t.Errorf("a malformed token is shown by the lexer's name: %q", got)
	}
}
