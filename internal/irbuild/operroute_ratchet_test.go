package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// maxOperatorNameTests is how many tests the operator-lowering code makes
// of a name where the checker's record of the operator
// (analysis.FileAnalysis.Operators) could answer instead. The code is
// every function in operatorLoweringFiles, and `binary` in irfuncbody.go.
// A test is:
//
//   - an `==` or `!=` with a non-empty string literal on one side and on
//     the other anything but an operator node's `Op` (an owner's, an
//     interface's or a method's name compared with a spelling; the
//     operator's own token is what the node is, not a name), and
//   - an index expression whose index is a string literal, or whose
//     indexed value is one of nameKeyedTables (a table keyed by a type's,
//     an interface's, an operator's or a method's name).
//
// A recorded operator reaches its function by declaration
// (recordedImplItem, stdFuncByID), so the count only goes down.
const maxOperatorNameTests = 6

// operatorLoweringFiles are the files that lower `a op b`, `a == b`,
// `a < b` and `~~~a`, and a qualified call of an operator interface's
// function, by their records.
var operatorLoweringFiles = []string{
	"iruseroperator.go", "irstdoperator.go", "operimpl.go",
	"irequality.go", "ircompare.go", "recordedimpl.go", "ifacecall.go",
}

// nameKeyedTables are the builder's tables keyed by a name: a field or
// variable of this name indexed by anything is a lookup by name. The impl
// tables (implsByIface, instImpls) are keyed by an interface's identity
// (ifaceKey), and std's byIface by its interface function's
// (analysis.StdFunc), and are not among them.
var nameKeyedTables = map[string]bool{
	"ownerImpls": true, "byType": true, "ifaces": true, "types": true,
	"genericTemplates": true, "items": true,
	"operIfaceByOp": true, "operIfaceByName": true,
}

// TestOperatorNameTestsOnlyGoDown counts the operator-lowering code's
// tests of a name and fails when the count rises, or falls without
// maxOperatorNameTests following it.
func TestOperatorNameTestsOnlyGoDown(t *testing.T) {
	fset := token.NewFileSet()
	var bodies []ast.Node
	for _, name := range append([]string{"irfuncbody.go"}, operatorLoweringFiles...) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || name == "irfuncbody.go" && fd.Name.Name != "binary" {
				continue
			}
			bodies = append(bodies, fd.Body)
		}
	}
	if len(bodies) < len(operatorLoweringFiles) {
		t.Fatalf("found %d function bodies in the operator-lowering files, so this test checks too little", len(bodies))
	}
	literal := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING
	}
	name := func(lit, other ast.Expr) bool {
		if !literal(lit) || lit.(*ast.BasicLit).Value == `""` {
			return false
		}
		sel, ok := other.(*ast.SelectorExpr)
		return !ok || sel.Sel.Name != "Op"
	}
	table := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.Ident:
			return nameKeyedTables[e.Name]
		case *ast.SelectorExpr:
			return nameKeyedTables[e.Sel.Name]
		}
		return false
	}
	var found []string
	for _, body := range bodies {
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if (n.Op == token.EQL || n.Op == token.NEQ) && (name(n.X, n.Y) || name(n.Y, n.X)) {
					found = append(found, fset.Position(n.Pos()).String())
				}
			case *ast.IndexExpr:
				if literal(n.Index) || table(n.X) {
					found = append(found, fset.Position(n.Pos()).String())
				}
			}
			return true
		})
	}
	switch {
	case len(found) > maxOperatorNameTests:
		t.Errorf("the operator-lowering code tests a name %d times, and at most %d may: reach an operator's function from its record (recordedOperatorImpl):\n  %s",
			len(found), maxOperatorNameTests, strings.Join(found, "\n  "))
	case len(found) < maxOperatorNameTests:
		t.Errorf("the operator-lowering code tests a name %d times: lower maxOperatorNameTests to %d", len(found), len(found))
	}
}
