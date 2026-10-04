package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// distinctBinding shares the wrapping projection and fresh-local delivery.
// The original statement owns its source position and Unit discard.
func (bl *irScalarBuilder) distinctBinding(t *ast.DistinctDestructure) bool {
	if t.Binding == nil || ast.IsDiscardName(t.Binding.Name) {
		irDeclineNote("a distinct destructure without a retained named binding")
		return false
	}
	name := t.Binding.Name
	if _, shadows := bl.g.lookup(name); shadows || bl.bound[name] != ir.NoTemp {
		irDeclineNote("a rebind: " + name)
		return false
	}
	src, k, _, ok := bl.lower(t.Value)
	if !ok || k.tag != tagNamed || !irDistinctOverValue(k.def) || t.TypeName != k.def.nomi {
		if ok {
			irDeclineNote("a distinct destructure outside the retained wrapping domain")
		}
		return false
	}
	inner := bl.distinctProjection(t, src, k, true, "irdistinctbinding.go distinctBinding")
	bind := ir.NewBind(bl.g.irNodePos(t), bl.f.NewTemp(), inner, bl.sh.localSym(name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: k.def.inner})
	bl.bound[name], bl.boundK[name] = bind.Dst(), k.def.inner
	return true
}
