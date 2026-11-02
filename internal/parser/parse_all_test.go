package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// ParseAll reports the first error of each declaration and of each
// statement inside a body, in source order, and nothing for a statement
// that only follows a damaged one.
func TestParseAll_ReportsEachStatementsError(t *testing.T) {
	src := "fn main() {\n    let a = 1\n    b := 2\n    ok = 3\n    dbg a\n}\n\nfn other() -> Int {\n    xs = [1\n    1\n}\n"
	_, errs := ParseAll(lexer.Lex(src))
	var lines []int
	for _, e := range errs {
		lines = append(lines, e.Line)
	}
	want := []int{2, 3, 10}
	if len(lines) != len(want) {
		t.Fatalf("errors on lines %v, want %v: %v", lines, want, errs)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("errors on lines %v, want %v: %v", lines, want, errs)
		}
	}
	if _, errs := ParseAll(lexer.Lex("fn main() {\n    dbg 1\n}\n")); errs != nil {
		t.Fatalf("a valid file has errors: %v", errs)
	}
}
