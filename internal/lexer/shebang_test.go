package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// A `#!` first line yields no token, and what follows keeps its own line
// and column.
func TestLex_ShebangFirstLineIsSkipped(t *testing.T) {
	assertTokens(t, "#!/usr/bin/env nomi\nx = 1\n", []token.Token{
		{Type: token.IDENT, Lexeme: "x", Line: 2, Col: 1},
		{Type: token.EQ, Line: 2, Col: 3},
		{Type: token.INT, Lexeme: "1", Line: 2, Col: 5},
		{Type: token.NEWLINE},
		{Type: token.EOF},
	})
	assertTokens(t, "#!/usr/bin/env nomi\r\n\n  y\n", []token.Token{
		{Type: token.IDENT, Lexeme: "y", Line: 3, Col: 3},
		{Type: token.NEWLINE},
		{Type: token.EOF},
	})
	assertTokens(t, "#!/usr/bin/env nomi", []token.Token{{Type: token.EOF}})
}

// `#!` anywhere but byte offset 0 is lexed as `#` and `!`, which the parser
// rejects (parser.TestParse_ShebangAfterFirstByteIsRejected).
func TestLex_ShebangElsewhereIsNotSkipped(t *testing.T) {
	for _, src := range []string{"x = 1\n#!/usr/bin/env nomi\n", " #!/usr/bin/env nomi\n", "\n#!/usr/bin/env nomi\n"} {
		hash := false
		for _, tok := range Lex(src) {
			if tok.Type == token.HASH {
				hash = true
			}
		}
		if !hash {
			t.Errorf("Lex(%q): want the #! lexed as a HASH token, not skipped", src)
		}
	}
}

func TestShebang(t *testing.T) {
	cases := map[string]string{
		"#!/usr/bin/env nomi\nfn main() {}\n": "#!/usr/bin/env nomi",
		"#!/usr/bin/env nomi\r\n":             "#!/usr/bin/env nomi",
		"#!nomi":                              "#!nomi",
		"// #!/usr/bin/env nomi\n":            "",
		" #!/usr/bin/env nomi\n":              "",
		"#[1, 2]\n":                           "",
	}
	for src, want := range cases {
		if got := Shebang(src); got != want {
			t.Errorf("Shebang(%q) = %q, want %q", src, got, want)
		}
	}
}
