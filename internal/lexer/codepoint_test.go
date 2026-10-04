package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/strlit"
	"github.com/nomi-language/nomi/internal/token"
)

// Every accepted spelling: the token keeps the body as written and
// strlit.DecodeCodepoint reads the value back.
func TestCodepointLiteralAccepted(t *testing.T) {
	cases := []struct {
		src  string
		body string
		want rune
	}{
		{`'a'`, `a`, 'a'},
		{`'Z'`, `Z`, 'Z'},
		{`'0'`, `0`, '0'},
		{`' '`, ` `, ' '},
		{`'~'`, `~`, '~'},
		{`'"'`, `"`, '"'},
		{`'$'`, `$`, '$'},
		{`'{'`, `{`, '{'},
		{`'/'`, `/`, '/'},
		{`'\n'`, `\n`, '\n'},
		{`'\t'`, `\t`, '\t'},
		{`'\\'`, `\\`, '\\'},
		{`'\''`, `\'`, '\''},
		{`'\u{0}'`, `\u{0}`, 0},
		{`'\u{D}'`, `\u{D}`, '\r'},
		{`'\u{41}'`, `\u{41}`, 'A'},
		{`'\u{7f}'`, `\u{7f}`, 0x7F},
		{`'\u{00007F}'`, `\u{00007F}`, 0x7F},
	}
	for _, c := range cases {
		toks := Lex(c.src)
		if len(toks) != 2 || toks[0].Type != token.CODEPOINT_LITERAL || toks[0].Lexeme != c.body {
			t.Errorf("Lex(%s) = %+v, want one CODEPOINT_LITERAL with body %q", c.src, toks, c.body)
			continue
		}
		if got, problem := strlit.DecodeCodepoint(toks[0].Lexeme); problem != "" || got != c.want {
			t.Errorf("DecodeCodepoint(%q) = %q, %q; want %q", toks[0].Lexeme, got, problem, c.want)
		}
	}
}

// The literal ends a statement, as any other literal does, and lexes
// beside operators with no space.
func TestCodepointLiteralInContext(t *testing.T) {
	assertTokens(t, "x = 'a'..='z'\ny", []token.Token{
		tok(token.IDENT, "x"),
		tok(token.EQ, "="),
		tok(token.CODEPOINT_LITERAL, "a"),
		tok(token.DOTDOTEQ, "..="),
		tok(token.CODEPOINT_LITERAL, "z"),
		tok(token.NEWLINE, "\n"),
		tok(token.IDENT, "y"),
		tok(token.EOF, ""),
	})
}

// Every rejected spelling, with its exact message. The token is ILLEGAL and
// spans the whole literal, so lexing resumes after it.
func TestCodepointLiteralRejected(t *testing.T) {
	cases := []struct {
		src     string
		problem string
	}{
		{`''`, `empty codepoint literal ''; a codepoint literal holds exactly one ASCII character`},
		{`'ab'`, `codepoint literal 'ab' holds more than one character; write "ab" as a string`},
		{`'\n\t'`, `codepoint literal '\n\t' holds more than one character; write "\n\t" as a string`},
		{`'é'`, `codepoint literal 'é' is U+00E9, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "é" as a string, or use Codepoint.from_int for another scalar value`},
		{`'😀'`, `codepoint literal '😀' is U+1F600, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "😀" as a string, or use Codepoint.from_int for another scalar value`},
		{`'\u{80}'`, `codepoint literal '\u{80}' is U+0080, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "\u{80}" as a string, or use Codepoint.from_int for another scalar value`},
		{`'\u{E9}'`, `codepoint literal '\u{E9}' is U+00E9, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "é" as a string, or use Codepoint.from_int for another scalar value`},
		{"'é'", "codepoint literal 'é' is U+0301, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write \"é\" as a string, or use Codepoint.from_int for another scalar value"},
		{`'\q'`, `invalid escape \q in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`},
		{`'\r'`, `invalid escape \r in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`},
		{`'\0'`, `invalid escape \0 in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`},
		{`'\$'`, `invalid escape \$ in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`},
		{`'\"'`, `invalid escape \" in codepoint literal; write '"' without a backslash`},
		{`'\u41'`, `\u escape requires braces, e.g. \u{41}`},
		{`'\u{41'`, `unterminated \u{...} escape`},
		{`'\u{}'`, `invalid \u{...} code point: ; a \u{...} escape takes 1 to 6 hex digits`},
		{`'\u{0000041}'`, `invalid \u{...} code point: 0000041; a \u{...} escape takes 1 to 6 hex digits`},
		{`'\u{110000}'`, `invalid \u{...} code point: 110000; the largest code point is 10FFFF`},
		{`'\u{D800}'`, `invalid \u{...} code point: D800; D800 to DFFF are surrogates, not Unicode scalar values`},
		{`'\u{zz}'`, `invalid \u{...} code point: zz; a \u{...} escape takes 1 to 6 hex digits`},
		{"'\t'", `codepoint literal holds control character U+0009 written directly; write it as the escape '\t'`},
		{"'\x07'", `codepoint literal holds control character U+0007 written directly; write it as the escape '\u{7}'`},
		{"'\x7f'", `codepoint literal holds control character U+007F written directly; write it as the escape '\u{7F}'`},
		{`'a`, `unterminated codepoint literal; close it with '`},
		{"'a\n'", `unterminated codepoint literal; close it with '`},
		{`'\'`, `unterminated codepoint literal; close it with '`},
		{`'`, `unterminated codepoint literal; close it with '`},
	}
	for _, c := range cases {
		toks := Lex(c.src)
		if len(toks) == 0 || toks[0].Type != token.ILLEGAL {
			t.Errorf("Lex(%q) = %+v, want an ILLEGAL token first", c.src, toks)
			continue
		}
		if toks[0].Problem != c.problem {
			t.Errorf("Lex(%q) problem:\n got %q\nwant %q", c.src, toks[0].Problem, c.problem)
		}
	}
}

// A rejected literal is one token: `'ab' + x` lexes the rest normally.
func TestCodepointLiteralRejectedSpansTheLiteral(t *testing.T) {
	toks := Lex(`'ab' + x`)
	got := types(toks)
	want := []token.TokenType{token.ILLEGAL, token.PLUS, token.IDENT, token.EOF}
	if len(got) != len(want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("types = %v, want %v", got, want)
		}
	}
	if toks[0].Lexeme != `'ab'` {
		t.Errorf("ILLEGAL lexeme = %q, want the whole literal", toks[0].Lexeme)
	}
}

// Strings keep their own rule: `\'` is not a string escape, as `\"` is not a
// codepoint escape. Neither quote needs escaping inside the other literal.
func TestStringRejectsSingleQuoteEscape(t *testing.T) {
	toks := Lex(`"\'"`)
	if toks[0].Type != token.ILLEGAL || toks[0].Lexeme != `invalid escape \'` {
		t.Errorf(`Lex("\'") = %+v, want ILLEGAL "invalid escape \'"`, toks[0])
	}
	assertTokens(t, `"it's"`, []token.Token{tok(token.STRING_LITERAL, "it's"), tok(token.EOF, "")})
}
