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
// has no impl, renders structurally with rt.DisplayText.
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
