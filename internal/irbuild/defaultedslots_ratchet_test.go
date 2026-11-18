package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxArgCountDefaultTests is how many places in this package's non-test
// files compare how many arguments a call writes with how many parameters
// its callee has. Such a comparison decides which parameters a call leaves
// to their defaults by counting, which assumes the written arguments fill
// the leading parameters; the checker's record says which slots are
// defaulted (argPlan.defaulted, leavesDefaults), and a trailing callback
// past a defaulted parameter breaks the assumption. The count only goes
// down.
//
// A site is a comparison that asks whether a call writes fewer arguments
// than its callee has parameters, or at least as many: len(A) < len(P),
// len(A) <= len(P), len(A) >= len(P), or any of them written the other way
// round (len(P) > len(A), ...). A is an expression whose last name is Args,
// args, temps or kinds (a call's arguments or the operands lowered from
// them), and P one whose last name contains "params" or "Params". A test
// for more arguments than parameters (len(A) > len(P)), which rejects a
// callee the call does not fit, decides no default and is not a site.
const maxArgCountDefaultTests = 0

// TestArgCountDefaultTestsOnlyGoDown counts the sites
// maxArgCountDefaultTests describes and fails when the count rises, or
// falls without the constant following it.
func TestArgCountDefaultTestsOnlyGoDown(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	lastName := func(e ast.Expr) string {
		switch e := e.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.SelectorExpr:
			return e.Sel.Name
		}
		return ""
	}
	lenOf := func(e ast.Expr) (string, bool) {
		c, ok := e.(*ast.CallExpr)
		if !ok || len(c.Args) != 1 {
			return "", false
		}
		if id, ok := c.Fun.(*ast.Ident); !ok || id.Name != "len" {
			return "", false
		}
		return lastName(c.Args[0]), true
	}
	isArgs := func(e ast.Expr) bool {
		n, ok := lenOf(e)
		return ok && (n == "Args" || n == "args" || n == "temps" || n == "kinds")
	}
	isParams := func(e ast.Expr) bool {
		n, ok := lenOf(e)
		return ok && (strings.Contains(n, "params") || strings.Contains(n, "Params"))
	}
	fset := token.NewFileSet()
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			argsFirst := isArgs(b.X) && isParams(b.Y)
			paramsFirst := isParams(b.X) && isArgs(b.Y)
			switch {
			case argsFirst && (b.Op == token.LSS || b.Op == token.LEQ || b.Op == token.GEQ),
				paramsFirst && (b.Op == token.GTR || b.Op == token.GEQ || b.Op == token.LEQ):
				found = append(found, fset.Position(b.Pos()).String())
			}
			return true
		})
	}
	switch {
	case len(found) > maxArgCountDefaultTests:
		t.Errorf("%d comparisons of a call's argument count with its callee's parameter count, and at most %d may: read the defaulted slots from the call's record (leavesDefaults, argPlan.defaulted):\n  %s",
			len(found), maxArgCountDefaultTests, strings.Join(found, "\n  "))
	case len(found) < maxArgCountDefaultTests:
		t.Errorf("%d comparisons of a call's argument count with its callee's parameter count: lower maxArgCountDefaultTests to %d", len(found), len(found))
	}
}
