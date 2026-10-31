package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// The bitwise operators are three characters each. `>>>` stays three `>`
// tokens, which close nested type arguments or, after an operand, the
// parser joins into the shift.
func TestBitwiseOperators(t *testing.T) {
	assertTokens(t, "a &&& b ||| c ^^^ ~~~d <<< 1", []token.Token{
		{Type: token.IDENT, Lexeme: "a"},
		{Type: token.BIT_AND, Lexeme: "&&&"},
		{Type: token.IDENT, Lexeme: "b"},
		{Type: token.BIT_OR, Lexeme: "|||"},
		{Type: token.IDENT, Lexeme: "c"},
		{Type: token.BIT_XOR, Lexeme: "^^^"},
		{Type: token.BIT_NOT, Lexeme: "~~~"},
		{Type: token.IDENT, Lexeme: "d"},
		{Type: token.SHL, Lexeme: "<<<"},
		{Type: token.INT, Lexeme: "1"},
		{Type: token.EOF},
	})
	assertTokens(t, "a >>> 1", []token.Token{
		{Type: token.IDENT, Lexeme: "a"},
		{Type: token.GT, Lexeme: ">", Col: 3},
		{Type: token.GT, Lexeme: ">", Col: 4},
		{Type: token.GT, Lexeme: ">", Col: 5},
		{Type: token.INT, Lexeme: "1"},
		{Type: token.EOF},
	})
}

// `|||` does not take `|>`'s bar, `^^^` leaves a lone `^` its own token, a
// capture's `&` and a placeholder are unchanged beside `&&&`, and the C
// spellings `<<`, `~` and `&&` are errors that name Nomi's.
func TestBitwiseOperatorsBesideTheirNeighbours(t *testing.T) {
	assertTokens(t, "a ||| b |> f()", []token.Token{
		{Type: token.IDENT}, {Type: token.BIT_OR}, {Type: token.IDENT}, {Type: token.PIPE},
		{Type: token.IDENT}, {Type: token.LPAREN}, {Type: token.RPAREN}, {Type: token.EOF},
	})
	assertTokens(t, "^ ^^^", []token.Token{{Type: token.CARET}, {Type: token.BIT_XOR}, {Type: token.EOF}})
	assertTokens(t, "&(&1 &&& 1)", []token.Token{
		{Type: token.CAPTURE}, {Type: token.LPAREN}, {Type: token.PLACEHOLDER, Lexeme: "&1"},
		{Type: token.BIT_AND}, {Type: token.INT}, {Type: token.RPAREN}, {Type: token.EOF},
	})
	for src, problem := range map[string]string{
		"a << 1": "Nomi spells shift left `<<<`: write `a <<< n`, not `a << n`",
		"~a":     "Nomi spells bitwise not `~~~`: write `~~~n`",
		"a && b": "Nomi spells logical and `and`: write `a and b`, not `a && b`",
	} {
		var found bool
		for _, tok := range Lex(src) {
			if tok.Type == token.ILLEGAL && tok.Problem == problem {
				found = true
			}
		}
		if !found {
			t.Errorf("Lex(%q) has no ILLEGAL token saying %q: %v", src, problem, Lex(src))
		}
	}
}

// A line that starts with `&&&`, `|||`, `^^^`, `<<<` or `>>>` continues the
// expression above it, so no NEWLINE comes before it; one that starts with
// `~~~`, which starts an expression, keeps its line break.
func TestBitwiseOperatorAtALineStart(t *testing.T) {
	for _, op := range []string{"&&&", "|||", "^^^", "<<<", ">>>"} {
		for _, tok := range Lex("x = a\n    " + op + " b\n") {
			if tok.Type == token.NEWLINE && tok.Line == 1 {
				t.Errorf("a line starting with %s ends the statement above it", op)
			}
		}
	}
	newline := false
	for _, tok := range Lex("x = a\n~~~b\n") {
		if tok.Type == token.NEWLINE && tok.Line == 1 {
			newline = true
		}
	}
	if !newline {
		t.Errorf("a line starting with ~~~ continues the statement above it")
	}
}
