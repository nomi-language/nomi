package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// ParseAll reports the first error of each declaration and of each
// statement inside a body, in source order, and nothing for a statement
// that only follows a damaged one.
func TestParseAll_ReportsEachStatementsError(t *testing.T) {
	src := "fn main()\n    let a = 1\n    b := 2\n    ok = 3\n    dbg a\nend\n\nfn other() -> Int\n    xs = [1\n    1\nend\n"
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
	if _, errs := ParseAll(lexer.Lex("fn main()\n    dbg 1\nend\n")); errs != nil {
		t.Fatalf("a valid file has errors: %v", errs)
	}
}

// A `(` a damaged statement left open ends at the end of the line the
// error is on: the statements after it parse, so the names they use are
// still used (an import they name is not reported unused). A `{` the error
// is inside keeps its lines (TestTap_BadOperandDoesNotCascade).
func TestParseAll_UnclosedParenEndsAtTheLine(t *testing.T) {
	src := "fn main()\n    let = (\n    io.print(a)\n    io.print(b)\n    io.print(after)\nend\n\nfn next() end\n"
	if _, errs := ParseAll(lexer.Lex(src)); len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	nodes, errs, _ := ParseResilient(lexer.Lex(src))
	names := map[string]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				names[id.Name] = true
			}
			return true
		})
	}
	if !names["after"] {
		t.Errorf("the statement after the damaged run was dropped: %v", errs)
	}
	if len(nodes) != 2 {
		t.Errorf("got %d declarations, want 2", len(nodes))
	}
}
