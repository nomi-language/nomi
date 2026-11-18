package irbuild

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Every node type of internal/ast has its forms in nodeForms, the table
// names no type the AST lacks, lowerNode's switch has an arm for exactly the
// types classed a value, and irScalarBlock's for exactly those classed a
// statement. A node type added to the AST fails here until it is classed,
// and one classed a value or a statement until its switch lowers it, so
// neither switch can meet a node it has no arm for.
func TestEveryNodeTypeHasAForm(t *testing.T) {
	inAST := map[string]bool{}
	var missing []string
	ast.NodeTypes(func(node string) {
		inAST[node] = true
		if nodeForms[node] == 0 {
			missing = append(missing, node)
		}
	})
	if len(inAST) < 100 {
		t.Fatalf("ast.NodeTypes lists only %d node types", len(inAST))
	}
	if len(missing) > 0 {
		t.Errorf("no form for %d node types; add each to nodeForms (nodeforms.go), and an arm to lowerNode for a value or to irScalarBlock for a statement:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	var stale []string
	for node := range nodeForms {
		if !inAST[node] {
			stale = append(stale, node)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("nodeForms classes node types the AST does not have:\n  %s", strings.Join(stale, "\n  "))
	}
	for _, sw := range []struct {
		fn, over string
		form     nodeForm
		class    string
	}{
		{"lowerNode", "n", formValue, "formValue"},
		{"irScalarBlock", "s", formStatement, "formStatement"},
	} {
		arms := typeSwitchArms(t, "irfuncbody.go", sw.fn, sw.over)
		var noArm, noForm []string
		for node, form := range nodeForms {
			if form&sw.form != 0 && !arms[node] {
				noArm = append(noArm, node)
			}
		}
		for node := range arms {
			if nodeForms[node]&sw.form == 0 {
				noForm = append(noForm, node)
			}
		}
		sort.Strings(noArm)
		sort.Strings(noForm)
		if len(noArm) > 0 {
			t.Errorf("%s has no arm for node types nodeForms classes %s:\n  %s", sw.fn, sw.class, strings.Join(noArm, "\n  "))
		}
		if len(noForm) > 0 {
			t.Errorf("%s has an arm for node types nodeForms does not class %s:\n  %s", sw.fn, sw.class, strings.Join(noForm, "\n  "))
		}
	}
}

// typeSwitchArms is the `*ast.X` types the type switch over the variable
// named over, in the function fn of file, has an arm for.
func typeSwitchArms(t *testing.T, file, fn, over string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	arms := map[string]bool{}
	found := false
	for _, d := range f.Decls {
		fd, ok := d.(*goast.FuncDecl)
		if !ok || fd.Name.Name != fn || fd.Body == nil {
			continue
		}
		goast.Inspect(fd.Body, func(n goast.Node) bool {
			ts, ok := n.(*goast.TypeSwitchStmt)
			if !ok {
				return true
			}
			var x goast.Expr
			switch a := ts.Assign.(type) {
			case *goast.AssignStmt:
				x = a.Rhs[0]
			case *goast.ExprStmt:
				x = a.X
			}
			ta, ok := x.(*goast.TypeAssertExpr)
			if !ok {
				return true
			}
			if id, ok := ta.X.(*goast.Ident); !ok || id.Name != over {
				return true
			}
			found = true
			for _, c := range ts.Body.List {
				for _, e := range c.(*goast.CaseClause).List {
					star, ok := e.(*goast.StarExpr)
					if !ok {
						continue
					}
					if sel, ok := star.X.(*goast.SelectorExpr); ok {
						arms[sel.Sel.Name] = true
					}
				}
			}
			return false
		})
	}
	if !found || len(arms) == 0 {
		t.Fatalf("%s: no type switch over %s in %s", file, over, fn)
	}
	return arms
}
