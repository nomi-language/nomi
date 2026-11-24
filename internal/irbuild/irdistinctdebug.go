package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// distinctDebugPlan is the Debug impl of a distinct or a marker: a written
// or derived one, or an opaque type's name-only one, called by its
// declaration identity, with the native frame captured by its Go wrapper.
// Nil for one with no Debug block, whose Debug is its shape
// (debugByShape), and for one the builder cannot call.
func (bl *irScalarBuilder) distinctDebugPlan(k kind) *irQualPlan {
	if p := bl.stdDebugPlan(k); p != nil {
		return p
	}
	if k.tag == tagNamed && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def) || irCompositeDistinct(k.def)) {
		// A distinct over a tuple renders through its derived or written
		// impl, `Coord(3, 4)`.
		return bl.structDebugPlan(k)
	}
	if !irWrappingDistinct(k.def) && !irRetainedMarker(k.def) {
		return nil
	}
	d := bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k]
	if d == nil && k.def.instOrigin == nil {
		// Another unit's type (a program file's, or a std module lowered
		// with the program, `Status`, `Toml`): the Debug impl its declaring
		// unit holds, as debugImplSym finds it.
		site, params, result, owning := bl.g.siblingImplMember(k.def, "inspect", stdIfaceImplKey(stdDebug))
		if site == nil || len(params) != 1 || params[0] != k || result != kindString {
			return nil
		}
		sym := owning.irImplSym(site.item)
		return &irQualPlan{token: site.item, name: sym.Name(), result: kindString, sym: sym}
	}
	if d == nil || !d.lowerable {
		return nil
	}
	it := d.items["inspect"]
	if it == nil || !it.lowerable || len(it.params) != 1 || it.params[0] != k || it.result != kindString {
		return nil
	}
	if _, ok := k.def.decl.(*ast.TypeDef); d.synth && !ok {
		return nil
	}
	if !bl.g.debugNamedInspects(k) {
		return nil
	}
	return &irQualPlan{token: it, name: k.nomi() + ".inspect", result: kindString}
}

// structDebugPlan calls a retained struct's or enum's Debug impl, derived or written,
// through the native named inspector.
func (bl *irScalarBuilder) structDebugPlan(k kind) *irQualPlan {
	d := bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k]
	if d == nil || !d.lowerable {
		return nil
	}
	it := d.items["inspect"]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil
	}
	if !bl.g.debugNamedInspects(k) {
		return nil
	}
	return &irQualPlan{token: it, name: k.nomi() + ".inspect", result: kindString}
}

// stdDebugPlan calls std's own retained `Debug.inspect` for a std named type
// with no local impl, such as calendar's Date.
func (bl *irScalarBuilder) stdDebugPlan(k kind) *irQualPlan {
	return bl.stdRenderPlanOf(bl.stdDebugAt(k), stdDebug, "inspect", k)
}

// stdDebugAt is std's own `Debug.inspect` at k, a std named type with no
// impl in this file, or nil.
func (bl *irScalarBuilder) stdDebugAt(k kind) *stdFunc {
	if k.tag != tagNamed || k.def == nil || bl.g.std == nil || bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k] != nil {
		return nil
	}
	return stdPick(bl.g.std.stdIfaceImpls(stdDebug.Func("inspect"), k), []kind{k})
}

// stdRenderPlanOf calls f, std's own rendering function method of iface
// (`Debug.inspect`, `Display.to_string`) at k, or nil when f is nil or has
// no body the builder can call.
func (bl *irScalarBuilder) stdRenderPlanOf(f *stdFunc, iface analysis.StdType, method string, k kind) *irQualPlan {
	// A body whose signature no gen shares (stdOutsideSubsetWhy) only an
	// abstract lowering builds (stdUnretainedMonoBody).
	// An impl an inherent method of the same spelling shadows
	// (`Bytes.to_string`) is named by its interface, which the shadow does
	// not reach (shadowedIfacePlan).
	shadowed := f != nil && f.why == shadowedByInherent
	if f == nil || (f.why != "" && f.why != stdOutsideSubsetWhy && !shadowed) {
		return nil
	}
	if s := bl.g.stdInsts; f.irBody == nil && s != nil && s.stdUnretainedMonoBody(f) {
		// A body the cache could not retain because it calls a generic
		// template (Regex's Debug calls `String.contains`): built per
		// program like an instance with no arguments, as a call to it is
		// (stdInstCallAt).
		inst, _ := s.instantiate(f, nil, nil, bl.g)
		if inst == nil || len(inst.view.params) != 1 || !bl.g.debugParamHolds(inst.view.params[0], k) || inst.view.result != kindString {
			return nil
		}
		return &irQualPlan{token: f, name: f.key, result: kindString, sym: inst.sym}
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irBody == nil || f.rtCall != "" {
		return nil
	}
	if shadowed {
		if len(f.params) != 1 || f.params[0] != k || f.result != kindString {
			return nil
		}
	} else if !bl.g.stdRenderResolves(iface, method, k) {
		return nil
	}
	return &irQualPlan{token: f, name: f.key, result: kindString, sym: f.irBody.Sym()}
}

// debugParamHolds reports whether a std Debug body whose parameter has
// kind param renders a value of kind k: the one kind, or in an abstract
// lowering, which nothing runs, a def of the one declaration. A type no
// gen shares (`Json.Case`) is a def of each gen's own, so the instance
// of its Debug body another instance's body names holds its own def.
func (g *gen) debugParamHolds(param, k kind) bool {
	if param == k {
		return true
	}
	return g.dc != nil && g.dc.abstract && param.tag == tagNamed && k.tag == tagNamed &&
		param.def != nil && k.def != nil && param.def.decl != nil && param.def.decl == k.def.decl
}
