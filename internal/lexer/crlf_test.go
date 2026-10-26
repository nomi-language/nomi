package lexer

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// A file saved with CRLF line ends (Git's checkout on Windows) lexes as the
// same file with LF: the same tokens, the same lexemes, multi-line strings
// included, at the same lines and columns.
func TestCRLFLexesAsLF(t *testing.T) {
	src := "import std/io\n\n// a comment\n/// a doc\nfn main() {\n  s = \"\"\"\n    a\n      b ${1 + 2}\n    \"\"\"\n  r = \"\"\"\n    raw\n    \"\"\"\n  b = `\n    one\n    two\n    `\n  io.print(s)\n}\n"
	lf := Lex(src)
	crlf := Lex(strings.ReplaceAll(src, "\n", "\r\n"))
	if len(lf) != len(crlf) {
		t.Fatalf("LF lexes to %d tokens and CRLF to %d", len(lf), len(crlf))
	}
	strs := 0
	for i := range lf {
		a, b := lf[i], crlf[i]
		// A line end starts one column later after a CR.
		sameCol := a.Col == b.Col || a.Type == token.NEWLINE || a.Type == token.BLANK_LINE
		if a.Type != b.Type || a.Lexeme != b.Lexeme || a.Line != b.Line || !sameCol {
			t.Errorf("token %d: LF %v %q %d:%d, CRLF %v %q %d:%d", i, a.Type, a.Lexeme, a.Line, a.Col, b.Type, b.Lexeme, b.Line, b.Col)
		}
		switch a.Type {
		case token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START, token.TRIPLE_STRING_END, token.RAW_TRIPLE_STRING_LITERAL:
			strs++
		}
	}
	if strs < 3 {
		t.Fatalf("the source lexes %d multi-line string tokens; the test checks the wrong thing", strs)
	}
}
