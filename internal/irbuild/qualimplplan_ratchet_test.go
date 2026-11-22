package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// maxQualImplPlanRoutes is how many routes qualImplPlan tries for a
// qualified call: the calls in its body of a builder method whose name
// ends in `Plan` (recordedCallPlan, stdIfacePlan, ...), each
// of which plans the call or declines it. The first two plan from the
// record: the function the call's record names (recordedCallPlan), and
// the interface function it dispatches on a receiver held as an interface
// value (dispatchedCallPlan). The others choose a function by the owner's
// spelling, the receiver's kind or a name-keyed table, and serve calls the
// record leaves to dispatch on a receiver of a known type, calls with no
// record, and calls whose function no gen registered where the call
// reaches it. A route a record-planned call no longer needs is deleted, so
// the count only goes down.
const maxQualImplPlanRoutes = 4

// TestQualImplPlanRoutesOnlyGoDown counts qualImplPlan's routes and fails
// when the count rises, or falls without maxQualImplPlanRoutes following
// it.
func TestQualImplPlanRoutesOnlyGoDown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "irqualcall.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "qualImplPlan" {
			body = fd.Body
		}
	}
	if body == nil {
		t.Fatal("irqualcall.go declares no qualImplPlan, so this test checks nothing")
	}
	var routes []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "Plan") {
			routes = append(routes, sel.Sel.Name)
		}
		return true
	})
	switch {
	case len(routes) > maxQualImplPlanRoutes:
		t.Errorf("qualImplPlan tries %d routes, and at most %d may: plan a call from the function its record names (recordedCallPlan):\n  %s",
			len(routes), maxQualImplPlanRoutes, strings.Join(routes, "\n  "))
	case len(routes) < maxQualImplPlanRoutes:
		t.Errorf("qualImplPlan tries %d routes: lower maxQualImplPlanRoutes to %d", len(routes), len(routes))
	}
}
