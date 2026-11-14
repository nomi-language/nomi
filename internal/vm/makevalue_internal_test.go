package vm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// makeValue runs every construction the compiler leaves to its handler, a
// cons among them, so a deferred call there is paid by every one
// (runtime.deferprocStack and _panic.nextDefer were 30% of its time). Only a
// Map or Set literal hashes through the key kernels that report a bad key
// by panicking, so only makeKeyed recovers. This counts the defer
// statements in each, since a defer allocates nothing a count of
// allocations would see.
func TestMakeValue_OnlyAKeyedLiteralDefers(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "records.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defers := map[string]int{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
			if _, isDefer := n.(*ast.DeferStmt); isDefer {
				defers[fd.Name.Name]++
			}
			return true
		})
	}
	if n := defers["makeValue"]; n != 0 {
		t.Errorf("makeValue has %d defer statement(s), want 0: every cons and update pays one", n)
	}
	if n := defers["makeKeyed"]; n != 1 {
		t.Errorf("makeKeyed has %d defer statement(s), want 1: `defer recoverKey(&err)`", n)
	}
}
