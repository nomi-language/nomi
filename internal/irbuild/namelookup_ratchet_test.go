package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The builder finds a declaration by the declaration the checker resolved,
// not by a name it spells again: a name looked up in this file's tables
// finds whatever this file binds it to, which is not always what the
// checker bound at the use (package doc, "Calls"). What still goes by name
// is counted here, and the counts only go down.
const (
	// maxNamedLookups is how many functions of this package find a
	// declaration from a name: those named `...Named` that take a string.
	maxNamedLookups = 22
	// maxNameTableReads is how many places read a name-keyed declaration
	// table by a name: `funcs[name]`, `types[name]`, `ifaces[name]`,
	// `onces[name]`.
	maxNameTableReads = 86
	// maxUncheckedSynthesizedBodies is how many sites lower code the
	// checker never checked (calls of markUnchecked): the universal Debug
	// of a type declared inside a body, parse_fields' per-field parse over
	// an anonymous struct, and madeIdent, which every identifier the
	// builder writes goes through.
	maxUncheckedSynthesizedBodies = 3
)

// TestNameLookupsOnlyGoDown counts the builder's lookups of a declaration
// by a spelled name and fails when either count rises, or falls without
// its maximum following it.
func TestNameLookupsOnlyGoDown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var named, reads, unchecked []string
	tables := map[string]bool{"funcs": true, "types": true, "ifaces": true, "onces": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if strings.HasSuffix(n.Name.Name, "Named") && takesString(n.Type) {
					named = append(named, name+": "+n.Name.Name)
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "markUnchecked" {
					unchecked = append(unchecked, fset.Position(n.Pos()).String())
				}
			case *ast.IndexExpr:
				if sel, ok := n.X.(*ast.SelectorExpr); ok && tables[sel.Sel.Name] {
					reads = append(reads, fset.Position(n.Pos()).String())
				}
			}
			return true
		})
	}
	check := func(what string, got []string, max int, constant string) {
		switch {
		case len(got) > max:
			t.Errorf("%d %s, and at most %d may be: find the declaration by the one the checker resolved (the call's analysis.Callee, a reference's symbol), not by its name:\n  %s",
				len(got), what, max, strings.Join(got, "\n  "))
		case len(got) < max:
			t.Errorf("%d %s: lower %s to %d", len(got), what, constant, len(got))
		}
	}
	check("functions find a declaration from a name", named, maxNamedLookups, "maxNamedLookups")
	check("reads of a name-keyed declaration table", reads, maxNameTableReads, "maxNameTableReads")
	check("sites lower code the checker never checked (markUnchecked)", unchecked, maxUncheckedSynthesizedBodies, "maxUncheckedSynthesizedBodies")
}

func takesString(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	for _, p := range ft.Params.List {
		if id, ok := p.Type.(*ast.Ident); ok && id.Name == "string" {
			return true
		}
	}
	return false
}
