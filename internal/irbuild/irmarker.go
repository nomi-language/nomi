package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// An absent inner kind can also describe an opaque host-backed payload.
// Require an actual zero-sized Nomi declaration before constructing its value.
func irRetainedMarker(d *typeDef) bool {
	// kindInvalid: marker — a payload-free declaration has no inner representation.
	if d == nil || !d.lowerable || !d.isDistinct || d.inner != kindInvalid || d.rtOpaque {
		return false
	}
	if irStdHostMarkerDef(d) {
		// std/bool's `True` and `False`: a `host type` with one inhabitant,
		// the singleton `Bool.False(x)` binds (stdHostSpecs' marker rows).
		return true
	}
	decl, ok := d.decl.(*ast.TypeDef)
	return ok && decl.InnerTypeExpr == nil
}

// irStdHostMarkerDef reports one of stdHostSpecs' `marker` rows' defs.
func irStdHostMarkerDef(d *typeDef) bool {
	for i, def := range stdHostDefs() {
		if def == d {
			return stdHostSpecs[i].marker
		}
	}
	return false
}

// markerValue lowers the type name at, spelled name, as its marker's one
// value: a bare `Quiet`, or `fakes.Quiet` through a whole-file import's
// qualifier, which namedType resolves to the same declaration.
func (bl *irScalarBuilder) markerValue(at ast.Node, name string) (ir.Temp, kind, bool, bool) {
	d, found := bl.g.namedType(name)
	// A std marker (`ChannelClosed`) has no declaration in this unit; its
	// def is stdgenhost.go's, which is a genuine zero-sized marker.
	if !found || !(irRetainedMarker(d) || irStdMarkerKind(named(d))) {
		return ir.NoTemp, kindInvalid, false, false
	}
	c := ir.NewMarker(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(d))
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: named(d)})
	return c.Dst(), named(d), true, true
}
