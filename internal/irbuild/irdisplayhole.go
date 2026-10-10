package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// displayHole renders an interpolation hole whose kind is outside the scalar
// domain through `Display.to_string`.
// The hole is already lowered into src, so the call is built over that
// operand rather than lowering the expression again.
//
// A std container or prelude enum calls std's impl instantiated at its kind
// (ifaceContainerCall), a declared type calls its own or std's impl
// (qualImplPlan), and an anonymous record or tuple of scalar leaves, which
// has no impl, renders structurally with rt.DisplayText. A record or tuple
// with any other part renders each part by Display first (displayComposite).
//
// A std body lowers the hole the same way. Only a user program may declare
// its own `Display`, which no hole renders through; inside std/display the
// local Display is std's.
func (bl *irScalarBuilder) displayHole(at ast.Node, src ir.Temp, k kind, mobile bool) (ir.Temp, bool) {
	if _, local := bl.g.types["Display"]; local && bl.g.stdModule == "" {
		return ir.NoTemp, false
	}
	structural := func() (ir.Temp, bool) {
		r := ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), true
	}
	if irStdDisplayExistential(k) {
		// `"${v}"` where v's static type is the interface Display: the VM
		// renders the value it holds through that value's own impl, as
		// `Display.to_string(v)` does.
		r := ir.NewRenderDisplayErased(bl.g.irNodePos(at), bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), true
	}
	if (irRetainedRecordKind(k) || irRetainedTupleKind(k)) && (irScalarLeafParts(k) || irStructuralDisplayKind(k)) {
		return structural()
	}
	if k == kindEmptyList || k == kindEmptyMap || k == kindEmptySet || k == kindEmptyVector {
		// `${[]}`: an empty container has no element to render through
		// Display, so its structural text is its Display text.
		return structural()
	}
	if _, vector := vectorElem(k); vector && !irRetainedVectorKind(k) && irStructuralDisplayKind(k) {
		// A Vector of Vectors, or of tuples: std's instance would render
		// each element through Display.to_string, which is structural here.
		return structural()
	}
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
	if irStructuralDisplayKind(k) {
		// A list or map of tuples or records, which std's instance does not
		// reach: every part is structural, so the whole is.
		return structural()
	}
	if irRetainedRecordKind(k) || irRetainedTupleKind(k) {
		return bl.displayComposite(at, src, k)
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

// displayComposite renders a tuple or anonymous record with a nominal part
// (`(1, None)`, `{a: 1, b: Some(2)}`), which rt.DisplayText cannot render
// alone because a part's text is its own Display impl's. The spec's rule is
// `(Display(a), Display(b), ...)` and `{f: Display(v), ...}`: each part is
// rendered by Display (displayPart), and the same-shaped composite of those
// Strings is rendered structurally, so rt's tuple and record layout is the
// one layout. A part with no rendering declines the whole.
func (bl *irScalarBuilder) displayComposite(at ast.Node, src ir.Temp, k kind) (ir.Temp, bool) {
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
		text, ok := bl.displayPart(at, p.Dst(), part)
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

// displayPart is one value's Display text, as an interpolation hole renders
// it: a String is itself, a scalar renders in place, and anything else goes
// through displayHole.
func (bl *irScalarBuilder) displayPart(at ast.Node, src ir.Temp, k kind) (ir.Temp, bool) {
	if k == kindString {
		return src, true
	}
	if irScalarLeafKind(k) || isDecimalKind(k) {
		r := ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), true
	}
	return bl.displayHole(at, src, k, true)
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

// irScalarLeafParts reports whether every component of a record or tuple
// kind is a scalar leaf.
func irScalarLeafParts(k kind) bool {
	if k.comp == nil {
		return false
	}
	for _, p := range k.comp.parts {
		if !irScalarLeafKind(p) {
			return false
		}
	}
	return true
}
