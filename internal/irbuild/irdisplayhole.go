package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// RENDERING A VALUE BY DISPLAY.
//
// The checker records how each interpolation hole's value renders
// (analysis.FileAnalysis.HoleDisplays): its own text, the machine's
// rendering of a scalar or a structural value, each part by its own
// record, the type's impl, or the impl an interface value holds.
// holeDisplay reads that record, and renderDisplay lowers one: it decides
// nothing about the route, and declines where the lowered kind is not the
// one the record describes.
//
// A hole of a type parameter is recorded as such: its rendering is its
// instance's, so at each instance it is the rendering of the instance's
// kind (kindDisplay), by the same rule the checker applies to a type. So is
// a value no hole holds, which the builder renders as a hole would:
// `io.print(x)`, `Display.to_string` over a tuple, a field's text for
// ParseFields and the error `main` returns.

// holeDisplay is the checker's record of how the interpolation hole at
// renders, at the hole's lowered kind k: for a hole of a type parameter,
// the rendering of k (kindDisplay). The checker records one for every hole it checks.
func (bl *irScalarBuilder) holeDisplay(at ast.Node, k kind) *analysis.DisplayRender {
	fa := bl.g.srcFA()
	if fa == nil {
		return nil
	}
	rec := fa.HoleDisplays[at]
	switch {
	case rec != nil && rec.Form == analysis.DisplayTypeParam:
		return kindDisplay(k)
	case rec == nil && k == kindString:
		// A body the checker never checked has no record: the universal
		// Debug of a block-local type, which the builder synthesizes
		// (registerBlockLocalDebug). Its holes are Strings, each its own
		// text.
		return &analysis.DisplayRender{Form: analysis.DisplayText}
	}
	return rec
}

// kindDisplay is how a value of kind k renders, for a value whose type
// the checker left to its instance: the checker's rule
// (analysis.DisplayRender) over the lowered kind.
func kindDisplay(k kind) *analysis.DisplayRender {
	r := &analysis.DisplayRender{Form: analysis.DisplayImpl}
	switch {
	case k == kindString:
		r.Form = analysis.DisplayText
	case irScalarLeafKind(k) || isDecimalKind(k):
		r.Form = analysis.DisplayScalar
	case irExistentialKind(k):
		r.Form = analysis.DisplayInterface
	case irStructuralDisplayKind(k) || irEmptyDisplayKind(k):
		r.Form = analysis.DisplayStructural
	case irRetainedRecordKind(k) || irRetainedTupleKind(k):
		r.Form = analysis.DisplayParts
		for _, p := range k.comp.parts {
			r.Parts = append(r.Parts, kindDisplay(p))
		}
	}
	return r
}

// displayValue renders an already-lowered value of kind k as its
// interpolation would (kindDisplay).
func (bl *irScalarBuilder) displayValue(at ast.Node, src ir.Temp, k kind) (ir.Temp, bool) {
	return bl.renderDisplay(at, src, k, kindDisplay(k))
}

// renderDisplay lowers rec, the rendering of src, a value of kind k, into
// its String. The operand is already lowered, so each route is built over
// it rather than lowering the expression again.
func (bl *irScalarBuilder) renderDisplay(at ast.Node, src ir.Temp, k kind, rec *analysis.DisplayRender) (ir.Temp, bool) {
	if rec == nil {
		panic("irbuild: an interpolation hole with no HoleDisplays record; the checker records one for every hole (analysis package doc, \"Elaboration\")")
	}
	render := func(r *ir.Render) (ir.Temp, bool) {
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), true
	}
	switch rec.Form {
	case analysis.DisplayText:
		return src, k == kindString
	case analysis.DisplayScalar:
		if !irScalarLeafKind(k) && !isDecimalKind(k) {
			return ir.NoTemp, false
		}
		return render(ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src))
	case analysis.DisplayStructural:
		if !irStructuralDisplayKind(k) && !irEmptyDisplayKind(k) {
			return ir.NoTemp, false
		}
		return render(ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src))
	case analysis.DisplayInterface:
		if !irExistentialKind(k) {
			// `(7 as Display)`: a value std's Display holds unboxed lowers
			// as itself (irErases), so the value it holds is the operand,
			// and it renders as that value's own type does.
			return bl.renderDisplay(at, src, k, kindDisplay(k))
		}
		if !irStdDisplayExistential(k) {
			return ir.NoTemp, false
		}
		// The VM renders the value the interface value holds through that
		// value's own impl, as `Display.to_string(v)` does.
		return render(ir.NewRenderDisplayErased(bl.g.irNodePos(at), bl.f.NewTemp(), src))
	case analysis.DisplayParts:
		return bl.displayParts(at, src, k, rec.Parts)
	case analysis.DisplayImpl:
		return bl.displayImpl(at, src, k)
	}
	// holeDisplay and displayParts resolve DisplayTypeParam to the
	// instance's rendering before it reaches here.
	panic("irbuild: a hole rendering of an unresolved form")
}

// irEmptyDisplayKind is an empty collection literal's kind, `[]`, `{}`,
// `[]` or `[]`: no element is rendered, so its text is structural.
func irEmptyDisplayKind(k kind) bool {
	return k == kindEmptyList || k == kindEmptyMap || k == kindEmptySet || k == kindEmptyVector
}

// displayImpl calls the impl of Display for k: std's instance for a std
// container (ifaceContainerCall), or a declared type's own or std's impl
// (qualImplPlan).
func (bl *irScalarBuilder) displayImpl(at ast.Node, src ir.Temp, k kind) (ir.Temp, bool) {
	line, col := nodePos(at)
	call := &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: "Display", Line: line, Col: col},
			Field:  &ast.Ident{Name: "to_string", Line: line, Col: col},
			Line:   line, Col: col,
		},
		Args: []ast.Node{at},
		Line: line,
		Col:  col,
	}
	args := irQualArgs{temps: []ir.Temp{src}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
	if v, rk, _, ok, handled := bl.ifaceContainerCall(call, args, "Display", "to_string"); handled {
		return v, ok && rk == kindString
	}
	if k.tag != tagNamed || k.def == nil {
		return ir.NoTemp, false
	}
	plan := bl.qualImplPlan(call, args, "Display", "to_string")
	if plan == nil || plan.result != kindString {
		return ir.NoTemp, false
	}
	v, _, _, ok := bl.qualEmit(call, args, plan)
	return v, ok
}

// displayParts renders a tuple or anonymous record whose parts are not all
// structural (`(1, None)`, `{a: 1, b: Some(2)}`): each part by its own
// record, and the same-shaped composite of those Strings structurally, so
// rt's tuple and record layout is the one layout. A part with no rendering
// declines the whole.
func (bl *irScalarBuilder) displayParts(at ast.Node, src ir.Temp, k kind, parts []*analysis.DisplayRender) (ir.Temp, bool) {
	if !irRetainedRecordKind(k) && !irRetainedTupleKind(k) || len(parts) != len(k.comp.parts) {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(at)
	if !irHeldValue(bl.f, src, bl.sides) {
		cp := ir.NewCopy(pos, bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyHold})
		src = cp.Dst()
	}
	record := k.tag == tagAnonStruct
	texts := make([]ir.Temp, len(k.comp.parts))
	strs := make([]kind, len(k.comp.parts))
	for i, part := range k.comp.parts {
		var p *ir.Proj
		if record {
			p = ir.NewProjRecordField(pos, bl.f.NewTemp(), src, k.comp.names[i], irParamShape(part))
		} else {
			p = ir.NewProjSlot(pos, bl.f.NewTemp(), src, i, irParamShape(part))
		}
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: part})
		rec := parts[i]
		if rec != nil && rec.Form == analysis.DisplayTypeParam {
			rec = kindDisplay(part)
		}
		text, ok := bl.renderDisplay(at, p.Dst(), part, rec)
		if !ok {
			return ir.NoTemp, false
		}
		texts[i], strs[i] = text, kindString
	}
	var made *ir.Make
	var mk kind
	if record {
		mk = bl.g.anonStructKind(k.comp.names, strs)
		made = ir.NewMakeRecord(pos, bl.f.NewTemp(), k.comp.names, texts)
	} else {
		mk = bl.g.tupleKind(strs)
		made = ir.NewMakeTuple(pos, bl.f.NewTemp(), texts)
	}
	bl.b.Append(made)
	bl.side(made.Dst(), irScalarSide{k: mk})
	r := ir.NewRenderDisplay(pos, bl.f.NewTemp(), made.Dst())
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst(), true
}

// irStructuralDisplayKind reports a kind whose Display text is structural all
// the way down: the four unnamed scalars, and records, tuples, lists and maps
// composed only of them, including the untyped `[]` and empty Map. No
// nominal type is reachable inside one, so no impl can answer for any part
// and rt.DisplayText is the text for the whole.
func irStructuralDisplayKind(k kind) bool {
	switch {
	case k == kindInt || k == kindFloat || k == kindString || k == kindBool:
		return true
	case k == kindEmptyList || k == kindEmptyMap:
		return true
	case irRetainedRecordKind(k) || irRetainedTupleKind(k):
		if k.comp == nil {
			return false
		}
		for _, p := range k.comp.parts {
			if !irStructuralDisplayKind(p) {
				return false
			}
		}
		return true
	case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
		return irStructuralDisplayKind(k.comp.parts[0])
	case k.tag == tagNamed:
		elem, vector := vectorElem(k)
		return vector && k != kindEmptyVector && irStructuralDisplayKind(elem)
	case k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2:
		return irStructuralDisplayKind(k.comp.parts[0]) && irStructuralDisplayKind(k.comp.parts[1])
	}
	return false
}
