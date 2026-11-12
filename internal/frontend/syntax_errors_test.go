package frontend

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A file's syntax errors are every one parser.ParseAll tells apart, the
// strict parse's first among them: two fields written as their names alone
// on the first error's line are two errors.
func TestAllSyntaxErrors_KeepsASecondPunOnTheFirstLine(t *testing.T) {
	src := "fn one(x: Int, y: Int) -> P\n    P{x, y}\nend\n\nfn two(x: Int) -> P\n    P{x, y: 1}\nend\n"
	_, err := parser.Parse(lexer.Lex(src))
	first, ok := err.(parser.ParseError)
	if !ok {
		t.Fatalf("Parse: %v, want a ParseError", err)
	}
	errs := allSyntaxErrors(src, first)
	type at struct{ line, col int }
	want := []at{{2, 7}, {2, 10}, {6, 7}}
	if len(errs) != len(want) {
		t.Fatalf("%d errors, want %d: %v", len(errs), len(want), errs)
	}
	for i, w := range want {
		if errs[i].Line != w.line || errs[i].Col != w.col {
			t.Errorf("error %d at %d:%d, want %d:%d", i, errs[i].Line, errs[i].Col, w.line, w.col)
		}
	}
}
