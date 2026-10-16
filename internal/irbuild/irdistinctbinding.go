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
	if ok && irSingleVariantPayload(k) {
		return bl.singleVariantBinding(t, src, k.def)
	}
	if !ok || k.tag != tagNamed || !irDistinctOverValue(k.def) {
		if ok {
			irDeclineNote("a distinct destructure outside the retained wrapping domain")
		}
		return false
	}
	inner := bl.distinctProjection(t, src, k, true, "irdistinctbinding.go distinctBinding")
	return bl.destructureBind(t, t.Binding.Name, t.Line, t.Col, k.def.inner, func() ir.Temp { return inner })
}

// singleVariantBinding lowers `E.V(x) = value` over an enum whose one variant
// is V: the pattern always matches, so x binds V's payload with no test, as
// a `case` arm's `E.V(x)` binds it. A struct-shaped V's payload is the record
// of its fields, and an embedded wrapping distinct's is the distinct's inner
// value (the checker's checkSingleVariantDestructure).
func (bl *irScalarBuilder) singleVariantBinding(t *ast.DistinctDestructure, src ir.Temp, d *typeDef) bool {
	v := &d.variants[0]
	var payload ir.Temp
	var pk kind
	if v.kind == "struct" {
		var ok bool
		if payload, pk, ok = bl.variantRecord(t, src, d, v); !ok {
			return false
		}
	} else {
		payload, pk = bl.variantPayload(t, src, d, v), v.payloads[0].k
		if irEmbedsValueDistinct(v) {
			payload = bl.distinctProjection(t, payload, pk, false, "irdistinctbinding.go singleVariantBinding")
			pk = v.embeds.inner
		}
	}
	return bl.destructureBind(t, t.Binding.Name, t.Line, t.Col, pk, func() ir.Temp { return payload })
}

// irSingleVariantPayload reports whether k is a retained enum with one
// variant that carries a payload: a struct-shaped variant's fields, or one
// value. A variant with none is the checker's error.
func irSingleVariantPayload(k kind) bool {
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) || len(k.def.variants) != 1 {
		return false
	}
	v := &k.def.variants[0]
	return v.kind == "struct" || len(v.payloads) == 1
}
