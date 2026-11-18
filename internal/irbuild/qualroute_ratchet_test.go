package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// maxTypeQualCallNameTests is how many tests typeQualCall's body makes of
// how a call is spelled: a comparison of the owner's name (`ti.Name`) or
// the method's with a string literal, and a lookup of the owner's name in
// this file's tables to see that a program's type does not shadow std's
// (`namedType(...)`, `g.types[...]`). A std route is keyed by what the
// call's record says it is qualified with instead (stdQualRoutes,
// qualRoute), and the count only goes down.
const maxTypeQualCallNameTests = 0

// TestTypeQualCallNameTestsOnlyGoDown counts typeQualCall's tests of a
// call's spelling and fails when the count rises, or falls without
// maxTypeQualCallNameTests following it.
func TestTypeQualCallNameTestsOnlyGoDown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "irqualcall.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "typeQualCall" {
			body = fd.Body
		}
	}
	if body == nil {
		t.Fatal("irqualcall.go declares no typeQualCall, so this test checks nothing")
	}
	spelled := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.Ident:
			return e.Name == "method"
		case *ast.SelectorExpr:
			id, ok := e.X.(*ast.Ident)
			return ok && id.Name == "ti" && e.Sel.Name == "Name"
		}
		return false
	}
	literal := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING
	}
	var found []string
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) &&
				(spelled(n.X) && literal(n.Y) || literal(n.X) && spelled(n.Y)) {
				found = append(found, fset.Position(n.Pos()).String())
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "namedType" {
				found = append(found, fset.Position(n.Pos()).String())
			}
		case *ast.IndexExpr:
			if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "types" {
				found = append(found, fset.Position(n.Pos()).String())
			}
		}
		return true
	})
	switch {
	case len(found) > maxTypeQualCallNameTests:
		t.Errorf("typeQualCall tests a call's spelling %d times, and at most %d may: route a std call by its record (stdQualRoutes):\n  %s",
			len(found), maxTypeQualCallNameTests, strings.Join(found, "\n  "))
	case len(found) < maxTypeQualCallNameTests:
		t.Errorf("typeQualCall tests a call's spelling %d times: lower maxTypeQualCallNameTests to %d", len(found), len(found))
	}
}
