package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Reading a subtype (spec §4 *Subtypes*, internal/analysis's
// subtype_reads.go). A call of a reader's function at a subtype reaches its
// base's impl, and the record the checker answers for it at the call's
// types (analysis.FileAnalysis.SubtypeReadAt) says how the value it makes
// is checked: each of the subtype's checks, `S.try_from(v)`, is called on
// the value, and the call answers what the base's impl answered when each
// is Some, and the record's breach when one is not. The value is not
// rebuilt: a subtype's value is its base's. A container needs no pass of
// its own, since its impl reads each element through the element's call,
// which is checked here in its own instance (subtypeinst.go). A record
// read from texts checks each field the same way where it reads it
// (irparsefields.go), its breach the record's None.

// subtypeReadOf is the read of a subtype t's record makes, or nil
// (analysis.FileAnalysis.SubtypeReadAt).
func (bl *irScalarBuilder) subtypeReadOf(t *ast.Call) *analysis.SubtypeRead {
	return bl.g.instanceFA().SubtypeReadAt(bl.g.calleeAt(t))
}

// readNarrowed is v, of kind rk, what t, a call whose record reads a subtype
// as read says, answered at the base, checked.
func (bl *irScalarBuilder) readNarrowed(t *ast.Call, read *analysis.SubtypeRead, v ir.Temp, rk kind) (ir.Temp, kind, bool, bool) {
	nv, ok := bl.narrowRead(t, v, rk, read)
	if !ok {
		bl.g.dc.note("a read of " + read.Expected + " whose check does not lower")
		return ir.NoTemp, kindInvalid, false, false
	}
	return nv, rk, false, true
}

// narrowRead is v, a read's answer of kind rk, an `Option` or a `Result` of
// the subtype's base, checked as read says. The checker names the breach
// by the shape of the function's result, so rk has the breach's variants.
func (bl *irScalarBuilder) narrowRead(t *ast.Call, v ir.Temp, rk kind, read *analysis.SubtypeRead) (ir.Temp, bool) {
	d := rk.def
	var okV, breachV *variantDef
	if d != nil {
		switch read.Breach {
		case analysis.BreachNone:
			okV, breachV = d.variant("Some"), d.variant("None")
		case analysis.BreachShapeError:
			okV, breachV = d.variant("Ok"), d.variant("Err")
		}
	}
	ty := bl.g.irTypeOf(rk)
	if rk.tag != tagNamed || okV == nil || breachV == nil || len(okV.payloads) != 1 || ty == nil ||
		read.Breach == analysis.BreachShapeError && len(breachV.payloads) != 1 {
		panic("irbuild: a read of a subtype whose result is no Option or Result of its base: " + rk.nomi())
	}
	base := okV.payloads[0].k
	pos := bl.g.irNodePos(t)
	slot := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: rk})
	cont := bl.f.NewBlock(pos, "subtype read")
	breach := bl.f.NewBlock(pos, "subtype rule broken")
	keep := func() {
		bl.b.Append(ir.NewCopy(pos, slot, v))
		bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	}

	made := bl.variantTest(t, v, d, okV)
	bl.b.Append(made)
	bl.side(made.Dst(), irScalarSide{k: kindBool})
	check := bl.f.NewBlock(pos, "subtype read made a value")
	other := bl.f.NewBlock(pos, "subtype read made none")
	bl.b.SetTerm(ir.NewBranch(pos, made.Dst(), check.ID(), other.ID()))
	bl.b = other
	keep()

	bl.b = check
	value := bl.variantPayload(t, v, d, okV)
	for i, c := range read.Checks {
		holds, ok := bl.subtypeHolds(t, i, value, base, c)
		if !ok {
			return ir.NoTemp, false
		}
		next := bl.f.NewBlock(pos, "subtype rule holds")
		bl.b.SetTerm(ir.NewBranch(pos, holds, next.ID(), breach.ID()))
		bl.b = next
	}
	keep()

	bl.b = breach
	var args []ir.Temp
	if read.Breach == analysis.BreachShapeError {
		got, ok := bl.debugTextAt(t, value, base, analysis.SubtypeBase(read.Type))
		if !ok {
			return ir.NoTemp, false
		}
		args = []ir.Temp{bl.shapeErrorAt(t, breachV.payloads[0].k, nil, bl.stringConst(pos, read.Expected), got)}
	}
	failed, _, _, ok := bl.variantValue(t, d, breachV, args, args == nil)
	if !ok {
		return ir.NoTemp, false
	}
	bl.b.Append(ir.NewCopy(pos, slot, failed))
	bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = cont
	return slot, true
}

// subtypeHolds is whether value, of the subtype's base kind k, satisfies
// c's rules: c's call `S.try_from(value)`, which the record names, is Some.
// i numbers c among its read's checks.
func (bl *irScalarBuilder) subtypeHolds(t *ast.Call, i int, value ir.Temp, k kind, c analysis.SubtypeCheck) (ir.Temp, bool) {
	name := "%subtype" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(i)
	key := bl.patternBinding(t, name, value, k)
	rc := analysis.PositionalCall(1, 1)
	rc.Callee = c.Call
	call := bl.g.synthIfaceCall(t.Line, analysis.Elaborate(c.Type).String(), "try_from", bl.g.madeRead(name, key, t.Line, 0), rc)
	m, mk, _, ok := bl.lower(call)
	if !ok {
		return ir.NoTemp, false
	}
	var some *variantDef
	if mk.def != nil {
		some = mk.def.variant("Some")
	}
	if mk.tag != tagNamed || some == nil {
		panic("irbuild: a subtype's try_from that answers no Option: " + mk.nomi())
	}
	test := bl.variantTest(t, m, mk.def, some)
	bl.b.Append(test)
	bl.side(test.Dst(), irScalarSide{k: kindBool})
	return test.Dst(), true
}

// debugTextAt is `Debug.inspect(value)` for value of kind k, of checker type
// vt: the call the record of that call at vt names.
func (bl *irScalarBuilder) debugTextAt(t *ast.Call, value ir.Temp, k kind, vt analysis.Type) (ir.Temp, bool) {
	name := "%subtypegot" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col)
	key := bl.patternBinding(t, name, value, k)
	call := bl.g.synthIfaceCall(t.Line, "Debug", "inspect", bl.g.madeRead(name, key, t.Line, 0), bl.g.stdIfaceCallRecord(stdDebug, "inspect", 1, vt))
	v, vk, _, ok := bl.lower(call)
	if ok && vk != kindString {
		panic("irbuild: Debug.inspect that answers no String: " + vk.nomi())
	}
	return v, ok
}
