package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// distinctDebugPlan resolves a distinct's actual Debug impl.
// Synthesized non-opaque defaults use structural rendering. A custom impl
// uses its declaration identity, with the native frame captured by its Go wrapper.
func (bl *irScalarBuilder) distinctDebugPlan(k kind) (*irQualPlan, bool) {
	if p := bl.stdDebugPlan(k); p != nil {
		return p, true
	}
	if k.tag == tagNamed && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def) || irCompositeDistinct(k.def)) {
		// A distinct over a tuple renders through its (derived or universal)
		// impl, `Coord(3, 4)`, which rt's structural Debug would spell
		// `Coord((3, 4))`.
		return bl.structDebugPlan(k)
	}
	if !irWrappingDistinct(k.def) && !irRetainedMarker(k.def) {
		return nil, false
	}
	d := bl.g.implsByIface["Debug"][k]
	if d == nil || !d.lowerable {
		return nil, false
	}
	it := d.items["inspect"]
	if it == nil || !it.lowerable || len(it.params) != 1 || it.params[0] != k || it.result != kindString {
		return nil, false
	}
	if d.synth {
		decl, ok := k.def.decl.(*ast.TypeDef)
		if !ok {
			return nil, false
		}
		if !decl.Opaque {
			return nil, true
		}
		// Opaque auto-Debug is name-only (`<opaque Meters>`): its
		// synthesized body is called, as a written impl's is, rather than
		// rendering the payload structurally.
	}
	ok := bl.g.debugNamedInspects(k)
	if !ok {
		return nil, false
	}
	return &irQualPlan{token: it, name: k.nomi() + ".inspect", result: kindString}, true
}

// structDebugPlan calls a retained struct's or enum's Debug impl, derived or written,
// through the native named inspector.
func (bl *irScalarBuilder) structDebugPlan(k kind) (*irQualPlan, bool) {
	d := bl.g.implsByIface["Debug"][k]
	if d == nil || !d.lowerable {
		return nil, false
	}
	it := d.items["inspect"]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil, false
	}
	ok := bl.g.debugNamedInspects(k)
	if !ok {
		return nil, false
	}
	return &irQualPlan{token: it, name: k.nomi() + ".inspect", result: kindString}, true
}

// stdDebugPlan calls std's own retained `Debug.inspect` for a std named type
// with no local impl, such as calendar's Date, through the inspector
// debugScalarInspector writes.
func (bl *irScalarBuilder) stdDebugPlan(k kind) *irQualPlan {
	if k.tag != tagNamed || k.def == nil || bl.g.std == nil || bl.g.implsByIface["Debug"][k] != nil {
		return nil
	}
	f := stdPick(bl.g.std.byIface["Debug.inspect"][k], []kind{k})
	if f == nil || f.why != "" {
		return nil
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irBody == nil || f.rtCall != "" {
		return nil
	}
	ok := bl.g.debugScalarInspects(k)
	if !ok {
		return nil
	}
	return &irQualPlan{token: f, name: f.key, result: kindString, sym: f.irBody.Sym()}
}
