package lexer

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// `&` and a number from 1 is one parameter token; `&` written against a
// name, `(`, `#`, `%`, `[` or `.` starts a capture. `&0` and a leading zero
// are errors that say how to write a parameter, and a bare `&` and `&&`
// keep their own errors.
func TestCaptureTokens(t *testing.T) {
	assertTokens(t, "&(&1 + &12)", []token.Token{
		tok(token.CAPTURE, "&"),
		tok(token.LPAREN, "("),
		tok(token.PLACEHOLDER, "&1"),
		tok(token.PLUS, "+"),
		tok(token.PLACEHOLDER, "&12"),
		tok(token.RPAREN, ")"),
		tok(token.EOF, ""),
	})
	for _, src := range []string{"&f(&1)", "&Int.max(&1, 0)", "&#Point{x: &1}", "&%{&1 => 0}", "&[&1]", "&.Circle(&1)"} {
		if toks := Lex(src); toks[0].Type != token.CAPTURE || toks[0].Lexeme != "&" {
			t.Errorf("Lex(%q) starts with %v, want the capture's `&`", src, toks[0])
		}
	}
	for _, src := range []string{"&0", "&01"} {
		toks := Lex(src)
		if toks[0].Type != token.ILLEGAL || !strings.Contains(toks[0].Problem, "numbered from 1") {
			t.Errorf("Lex(%q) = %v, want an ILLEGAL token saying a parameter is numbered from 1", src, toks[0])
		}
	}
	if toks := Lex("a && b"); toks[1].Type != token.ILLEGAL || !strings.Contains(toks[1].Problem, "a and b") {
		t.Errorf("`&&` lexes as %v, want its own error", toks[1])
	}
	for _, src := range []string{"& x", "& (x)"} {
		if toks := Lex(src); toks[0].Type != token.ILLEGAL || toks[0].Lexeme != "&" {
			t.Errorf("%q lexes as %v, want a bare `&`, ILLEGAL", src, toks[0])
		}
	}
}
