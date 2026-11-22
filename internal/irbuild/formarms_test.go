package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestEveryOperatorFormHasAnArm holds each family's switch over the
// checker's record to the closed set of OperatorForms
// (analysis.OperatorForms): formArms gives every form of every family an
// arm or the reason it has none, and the family's switch (familySwitches)
// reads, among the forms, exactly the ones formArms gives an arm. A form
// added to the checker fails here, by name, for every family, until each
// says what it does with it; one given an arm fails until its switch
// reads it. A form with no arm declines and is counted (unreadAnswers),
// so the census has a class, and a name, for it.
func TestEveryOperatorFormHasAnArm(t *testing.T) {
	forms := analysis.OperatorForms()
	consts := operatorFormConsts(t)
	if len(forms) < 4 || len(consts) != len(forms) {
		t.Fatalf("analysis.OperatorForms lists %d forms and operator_uses.go declares %d exported ones, so this test checks too little", len(forms), len(consts))
	}
	if len(familySwitches) != len(formArms) {
		t.Errorf("familySwitches names %d families and formArms %d", len(familySwitches), len(formArms))
	}
	bodies := irbuildFuncBodies(t)
	for _, f := range sortedFamilies() {
		arms, listed := formArms[f]
		if !listed {
			t.Errorf("formArms has no row for %s", f)
			continue
		}
		reads := analysisNamesIn(bodies[familySwitches[f]])
		if reads == nil {
			t.Errorf("%s: internal/irbuild declares no function %s, or more than one", f, familySwitches[f])
			continue
		}
		for _, form := range forms {
			name := consts[form]
			a, given := arms[form]
			switch {
			case !given:
				t.Errorf("%s: formArms gives %s no arm and no reason it has none: add one to formArms (operatorform.go), and the arm to %s", f, name, familySwitches[f])
			case (a.arm == "") == (a.none == ""):
				t.Errorf("%s: formArms gives %s both an arm and a reason it has none, or neither", f, name)
			case a.arm != "" && !reads[name]:
				t.Errorf("%s: formArms gives %s an arm, and %s does not read it", f, name, familySwitches[f])
			case a.none != "" && reads[name]:
				t.Errorf("%s: formArms gives %s no arm, and %s reads it", f, name, familySwitches[f])
			}
			if operatorAnswerNames[formAnswer(form)] == "" {
				t.Errorf("the operator census has no name for the answer of %s", name)
			}
		}
	}
}

// sortedFamilies is every family familySwitches names, in order.
func sortedFamilies() []operatorFamily {
	var out []operatorFamily
	for f := range familySwitches {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// operatorFormConsts names each OperatorForm by its exported constant in
// internal/analysis/operator_uses.go, read from the declaration: the
// constants of the block whose first is typed OperatorForm, valued from
// one up in order.
func operatorFormConsts(t *testing.T) map[analysis.OperatorForm]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "analysis", "operator_uses.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[analysis.OperatorForm]string{}
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST || len(gd.Specs) == 0 {
			continue
		}
		first := gd.Specs[0].(*ast.ValueSpec)
		if id, ok := first.Type.(*ast.Ident); !ok || id.Name != "OperatorForm" {
			continue
		}
		for i, spec := range gd.Specs {
			name := spec.(*ast.ValueSpec).Names[0]
			if name.IsExported() {
				out[analysis.OperatorForm(i+1)] = name.Name
			}
		}
	}
	return out
}

// irbuildFuncBodies is each function and method this package's non-test
// files declare, by name; a name declared twice has a nil body.
func irbuildFuncBodies(t *testing.T) map[string]*ast.BlockStmt {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.BlockStmt{}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if seen[fd.Name.Name] {
				out[fd.Name.Name] = nil
				continue
			}
			seen[fd.Name.Name] = true
			out[fd.Name.Name] = fd.Body
		}
	}
	return out
}

// analysisNamesIn is the names body reads from package analysis
// (`analysis.OperatorByImpl`), nil for a nil body.
func analysisNamesIn(body *ast.BlockStmt) map[string]bool {
	if body == nil {
		return nil
	}
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "analysis" {
				out[sel.Sel.Name] = true
			}
		}
		return true
	})
	return out
}
