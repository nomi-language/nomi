package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// distinctBinding shares the wrapping projection and destructureBind's
// fresh-local delivery and rebind rule. The original statement owns its
// source position and Unit discard.
//
// The written type name (`Id`, `calendar.Days`) selects nothing: the checker
// requires it to name the value's distinct type and types the binding as
// that type's inner value, so the value's kind is the one to unwrap.
func (bl *irScalarBuilder) distinctBinding(t *ast.DistinctDestructure) bool {
	if t.Binding == nil || ast.IsDiscardName(t.Binding.Name) {
		irDeclineNote("a distinct destructure without a retained named binding")
		return false
	}
	src, k, _, ok := bl.lower(t.Value)
	if !ok || k.tag != tagNamed || !irDistinctOverValue(k.def) {
		if ok {
			irDeclineNote("a distinct destructure outside the retained wrapping domain")
		}
		return false
	}
	inner := bl.distinctProjection(t, src, k, true, "irdistinctbinding.go distinctBinding")
	return bl.destructureBind(t, t.Binding.Name, t.Line, t.Col, k.def.inner, func() ir.Temp { return inner })
}
