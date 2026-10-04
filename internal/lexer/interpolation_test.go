package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// Only `${` is special in a string: it opens an interpolation, and `\${`
// writes it as text. `#`, `#{`, `##`, `$` and `$$` are ordinary text.
func TestInterpolationOpenerIsTheOnlySpecialPair(t *testing.T) {
	body := `# ## #{x} $ $$ \${old} $logger`
	want := `# ## #{x} $ $$ ${old} $logger`
	for _, test := range []struct {
		name, open, close string
		kind              token.TokenType
	}{
		{"quoted", `"`, `"`, token.STRING_LITERAL},
		{"triple", `"""`, `"""`, token.TRIPLE_STRING_LITERAL},
		{"typed", `Tag"`, `"`, token.TAGGED_STRING_LITERAL},
		{"typed triple", `Tag"""`, `"""`, token.TAGGED_TRIPLE_STRING_LITERAL},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := Lex(test.open + body + test.close)
			if len(tokens) != 2 || tokens[0].Type != test.kind || tokens[0].Lexeme != want {
				t.Fatalf("tokens: %#v", tokens)
			}
		})
	}
}

// A `$` before an interpolation is text.
func TestInterpolationAfterDollar(t *testing.T) {
	assertTokens(t, `"$${port}"`, []token.Token{
		tok(token.STRING_START, "$"),
		{Type: token.IDENT, Lexeme: "port", Line: 1, Col: 5},
		tok(token.STRING_END, ""), tok(token.EOF, ""),
	})
}

// A set literal inside an interpolation is code.
func TestInterpolationHoldsASetLiteral(t *testing.T) {
	tokens := Lex(`"${#{1}}"`)
	if tokens[0].Type != token.STRING_START || tokens[1].Type != token.HASH || tokens[2].Type != token.LBRACE {
		t.Fatalf("tokens: %#v", tokens)
	}
}

func TestRawStringsKeepInterpolationText(t *testing.T) {
	body := `#{x} ## $ ${x} \${x} $$ \n`
	for _, test := range []struct {
		name, open, close string
		kind              token.TokenType
	}{
		{"raw", "`", "`", token.RAW_STRING_LITERAL},
		{"raw multiline", "`\n  ", "\n  `", token.RAW_TRIPLE_STRING_LITERAL},
		{"typed raw", "Tag`", "`", token.RAW_TAGGED_STRING_LITERAL},
		{"typed raw multiline", "Tag`\n  ", "\n  `", token.RAW_TAGGED_TRIPLE_STRING_LITERAL},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens := Lex(test.open + body + test.close)
			if len(tokens) != 2 || tokens[0].Type != test.kind || tokens[0].Lexeme != body {
				t.Fatalf("tokens: %#v", tokens)
			}
		})
	}
}

// `\$` is an escape only before `{`, and `\#` is no escape at all. In a
// triple-quoted string `\${` is the one backslash sequence; every other
// backslash is text.
func TestDollarEscapeOnlyBeforeABrace(t *testing.T) {
	for _, source := range []string{`"\#"`, `"\$"`, `"\$x"`} {
		tokens := Lex(source)
		if tokens[0].Type != token.ILLEGAL {
			t.Fatalf("%s accepted: %#v", source, tokens)
		}
	}
	assertTokens(t, `"""\${x} \#{y} \$z \\${w}"""`, []token.Token{tok(token.TRIPLE_STRING_LITERAL, `${x} \#{y} \$z \${w}`), tok(token.EOF, "")})
}
