package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// maxDisplayToStringCallSites is how many places this package's non-test
// files write the callee of a call `Display.to_string(x)` as syntax: a
// composite literal of ast.FieldAccess (`&ast.FieldAccess{...}` or
// `ast.FieldAccess{...}`) whose Object is a composite literal of
// ast.TypeIdent with Name "Display" and whose Field is a composite literal
// of ast.Ident with Name "to_string". A call so written reaches its impl
// by the receiver kind's name (qualImplPlan). A value whose rendering the
// checker recorded renders from that record (renderDisplay, displayImpl),
// and the count only goes down. The one left is displayImpl's, for a
// rendering made from a kind (kindDisplay).
const maxDisplayToStringCallSites = 1

// TestDisplayToStringCallSitesOnlyGoDown counts the places this package
// writes a `Display.to_string` callee and fails when the count rises, or
// falls without maxDisplayToStringCallSites following it.
func TestDisplayToStringCallSitesOnlyGoDown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	// named reports a composite literal of the nomi ast type typ, written
	// with or without `&`, whose Name field is the string literal name.
	named := func(e ast.Expr, typ, name string) bool {
		if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
			e = u.X
		}
		lit, ok := e.(*ast.CompositeLit)
		if !ok {
			return false
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != typ {
			return false
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			val, isLit := kv.Value.(*ast.BasicLit)
			if ok && isLit && key.Name == "Name" && val.Kind == token.STRING {
				if s, err := strconv.Unquote(val.Value); err == nil && s == name {
					return true
				}
			}
		}
		return false
	}
	field := func(lit *ast.CompositeLit, key string) ast.Expr {
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == key {
					return kv.Value
				}
			}
		}
		return nil
	}
	var found []string
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
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if sel, ok := lit.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "FieldAccess" {
				return true
			}
			obj, fld := field(lit, "Object"), field(lit, "Field")
			if obj != nil && fld != nil && named(obj, "TypeIdent", "Display") && named(fld, "Ident", "to_string") {
				found = append(found, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	switch {
	case len(found) > maxDisplayToStringCallSites:
		t.Errorf("this package writes a `Display.to_string` call %d times, and at most %d may: render a value from the checker's record of its rendering (renderDisplay):\n  %s",
			len(found), maxDisplayToStringCallSites, strings.Join(found, "\n  "))
	case len(found) < maxDisplayToStringCallSites:
		t.Errorf("this package writes a `Display.to_string` call %d times: lower maxDisplayToStringCallSites to %d", len(found), len(found))
	}
}
