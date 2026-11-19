package docscheck

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// A TestMain ends with os.Exit, which runs no deferred call, so a call
// TestMain defers itself never runs: a temporary directory it removes that
// way is left behind by every run of the package's tests. The form that
// works is a function that does the work, defers its cleanup and answers
// the code, and a TestMain that exits with it (cmd/nomi's runTests). This
// test fails for a tracked TestMain that both defers a call and calls
// os.Exit. A deferred call inside a function literal TestMain makes is that
// literal's, and does not count.
func TestTestMainDefersNothingItExitsPast(t *testing.T) {
	root, files := trackedFilesMatching(t, "*_test.go")
	checked := 0
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(src, []byte("func TestMain(")) {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
				continue
			}
			checked++
			if pos := deferPastExit(fn.Body); pos.IsValid() {
				t.Errorf("%s: TestMain defers a call and calls os.Exit, which runs no deferred call; "+
					"move the work and its defer into a function that answers the exit code", fset.Position(pos))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no TestMain found in the tracked tests; the scan is wrong")
	}
	t.Logf("%d TestMain functions checked", checked)
}

// TestTestMainDefersNothingItExitsPast_TheScanFindsADefer holds the scan to
// the shape it reports and the ones it leaves.
func TestTestMainDefersNothingItExitsPast_TheScanFindsADefer(t *testing.T) {
	for _, c := range []struct {
		body    string
		reports bool
	}{
		{"dir, _ := os.MkdirTemp(\"\", \"x\")\ndefer os.RemoveAll(dir)\nos.Exit(m.Run())", true},
		{"os.Exit(run(m))", false},
		{"defer cleanup()\nm.Run()", false},
		{"f := func() int { defer cleanup(); return m.Run() }\nos.Exit(f())", false},
	} {
		src := "package p\nfunc TestMain(m *testing.M) {\n" + c.body + "\n}\n"
		f, err := parser.ParseFile(token.NewFileSet(), "p_test.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		body := f.Decls[0].(*ast.FuncDecl).Body
		if got := deferPastExit(body).IsValid(); got != c.reports {
			t.Errorf("TestMain {\n%s\n}: reported %v, want %v", c.body, got, c.reports)
		}
	}
}

// deferPastExit answers the position of the first call body defers when
// body also calls os.Exit, and an invalid position otherwise. Function
// literals are not entered: their defers run when they return.
func deferPastExit(body *ast.BlockStmt) token.Pos {
	var deferAt token.Pos
	exits := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			if !deferAt.IsValid() {
				deferAt = n.Pos()
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Exit" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
					exits = true
				}
			}
		}
		return true
	})
	if exits {
		return deferAt
	}
	return token.NoPos
}
