package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// sameImplPlan follows native bare-call resolution only after file functions
// have had their opportunity. Ambiguous interface requirements keep the native
// diagnostic instead of being silently selected by retention.
// sameImplName reports whether a bare call to name may resolve to the
// enclosing impl block's own item: nothing at module scope binds the name, or
// what binds it is the block's interface's own default (`shout(value)` in
// `Formatted`'s `banner`, which the analyzer scopes as Formatted's).
func (bl *irScalarBuilder) sameImplName(id *ast.Ident) bool {
	name := id.Name
	sym := resolvedBareSymbolAt(bl.g.fa, id)
	if sym == nil {
		return true
	}
	d := bl.g.implBlock
	return d != nil && d.iface != nil && d.iface.decl != nil && sym.OwningInterface != "" &&
		sym.OwningInterface == d.iface.decl.Name && d.iface.methods[name] != nil
}

func (bl *irScalarBuilder) sameImplPlan(name string) *irQualPlan {
	d := bl.g.implBlock
	if d == nil || !d.lowerable {
		return nil
	}
	if d.iface != nil && d.iface.methods[name] != nil && bl.g.rivalRequirementOwner(d, name) != "" {
		return nil
	}
	it := d.items[name]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil
	}
	return &irQualPlan{token: it, name: d.recv.nomi() + "." + name, result: it.result}
}
