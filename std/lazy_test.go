package std

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestShared_ImplsAreBuiltWithWhatTheyImplement holds the rule that lets a
// stdlib built a module at a time answer as the whole one does: a program's
// impl index holds every impl it could select, so an impl of an interface
// for a type is built whenever both are. That holds when the impl's module
// is the interface's or the type's, or one either of them imports, or one
// the stdlib is first built with (stdBaseModules). std is one Nomi module,
// so its orphan rule would allow an impl anywhere else; this is what keeps
// it out.
func TestShared_ImplsAreBuiltWithWhatTheyImplement(t *testing.T) {
	lib := All()
	c := lib.core
	if c == nil {
		t.Fatal("the shared stdlib is not built a module at a time")
	}
	declIn := map[ast.Node]string{}
	for name, nodes := range lib.Nodes {
		for _, n := range nodes {
			declIn[n] = name
		}
	}
	// moduleOf is the module declaring the type te names in module m, or ""
	// when it names none (a type parameter, a function type).
	moduleOf := func(m string, te ast.TypeExpr) string {
		var name string
		switch v := te.(type) {
		case *ast.SimpleType:
			name = v.Name
		case *ast.GenericType:
			name = v.Name
		case *ast.QualifiedType:
			// `Json.ShapeError`, a type its owner's module declares, or
			// `json.Json`, a module's.
			name = v.Module
		default:
			return ""
		}
		sym := lib.Files[m].ModuleScope.Lookup(name)
		if sym == nil {
			return ""
		}
		for sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym.ModuleScope != nil {
			for mod, scope := range lib.Modules {
				if scope == sym.ModuleScope {
					return mod
				}
			}
		}
		return declIn[sym.Node]
	}
	base := map[string]bool{}
	for _, name := range c.closure(stdBaseModules, nil) {
		base[name] = true
	}
	imports := func(from, mod string) bool {
		return slices.Contains(c.closure([]string{from}, nil), mod)
	}
	checked := 0
	for m, nodes := range lib.Nodes {
		for _, n := range nodes {
			ib, ok := n.(*ast.ImplBlock)
			if !ok || ib.Interface == nil {
				continue
			}
			checked++
			iface, recv := moduleOf(m, ib.Interface), moduleOf(m, ib.Receiver)
			if base[m] || m == iface || m == recv || imports(iface, m) || imports(recv, m) {
				continue
			}
			t.Errorf("std/%s:%d implements an interface of std/%s for a type of std/%s, and neither imports it: a program reaching both would not have the impl built", m, ib.Line, iface, recv)
		}
	}
	if checked < 100 {
		t.Fatalf("checked %d impls; the walk is not reading the stdlib's", checked)
	}
}
