package irbuild

import (
	"maps"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// std/parse's `ParseFields` at an anonymous struct (spec §12 *Tuples,
// anonymous structs and functions*). No impl can be written for an
// anonymous struct, so its three functions are lowered here, where the
// instance binds `P` to the record, as `Hashable.hash` over a record is
// (irtuplehash.go):
//
//   - `P.field_names()` is the list of its field names, in the canonical
//     (sorted) order a record's kind keeps them in;
//   - `P.field_texts(p)` is each field's Display text, in that order;
//   - `P.parse_fields(texts)` reads text i with field i's type's
//     `Parse.parse`, and is `Some` of the record when every one is `Ok`,
//     and `None` at the first that is not.
//
// The checker admits the record only when each field implements `Parse`
// and `Display` (analysis/structural_parse_fields.go).

// parseFieldsCall lowers t, a call of one of ParseFields' functions at the
// record kind k. handled is false for any other call.
func (bl *irScalarBuilder) parseFieldsCall(t *ast.Call, k kind, method string) (ir.Temp, kind, bool, bool, bool) {
	unit := k == kindUnit
	if !unit && (k.tag != tagAnonStruct || k.comp == nil) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || bl.boundCallIface(fa) != "ParseFields" {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if unit {
		// Unit has no fields: std's impl for it, which no call can name
		// by a receiver, written here.
		switch method {
		case "field_names", "field_texts":
			if method == "field_texts" {
				if len(t.Args) != 1 {
					return ir.NoTemp, kindInvalid, false, false, true
				}
				if _, _, _, ok := bl.lower(t.Args[0]); !ok {
					return ir.NoTemp, kindInvalid, false, false, true
				}
			}
			v, rk, ok := bl.stringList(t, nil)
			return v, rk, false, ok, true
		case "parse_fields":
			v, rk, ok := bl.unitParseFields(t)
			return v, rk, false, ok, true
		}
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch method {
	case "field_names":
		v, rk, ok := bl.parseFieldNames(t, k)
		return v, rk, false, ok, true
	case "field_texts":
		v, rk, ok := bl.parseFieldTexts(t, k)
		return v, rk, false, ok, true
	case "parse_fields":
		v, rk, ok := bl.parseFieldsOf(t, k)
		return v, rk, false, ok, true
	}
	return ir.NoTemp, kindInvalid, false, false, false
}

func (bl *irScalarBuilder) stringList(t *ast.Call, items []ir.Temp) (ir.Temp, kind, bool) {
	lk := bl.g.listKind(kindString)
	list := ir.NewMakeList(bl.g.irNodePos(t), bl.f.NewTemp(), items, ir.NoTemp)
	bl.b.Append(list)
	bl.side(list.Dst(), irScalarSide{k: lk})
	return list.Dst(), lk, true
}

func (bl *irScalarBuilder) parseFieldNames(t *ast.Call, k kind) (ir.Temp, kind, bool) {
	pos := bl.g.irNodePos(t)
	items := make([]ir.Temp, len(k.comp.names))
	for i, name := range k.comp.names {
		c := ir.NewString(pos, bl.f.NewTemp(), name)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindString})
		items[i] = c.Dst()
	}
	return bl.stringList(t, items)
}

func (bl *irScalarBuilder) parseFieldTexts(t *ast.Call, k kind) (ir.Temp, kind, bool) {
	if len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false
	}
	subj, sk, _, ok := bl.lower(t.Args[0])
	if !ok || sk != k {
		return ir.NoTemp, kindInvalid, false
	}
	pos := bl.g.irNodePos(t)
	items := make([]ir.Temp, len(k.comp.parts))
	for i, part := range k.comp.parts {
		p := ir.NewProjRecordField(pos, bl.f.NewTemp(), subj, k.comp.names[i], irParamShape(part))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: part})
		text, ok := bl.displayPart(t, p.Dst(), part)
		if !ok {
			return ir.NoTemp, kindInvalid, false
		}
		items[i] = text
	}
	return bl.stringList(t, items)
}

func (bl *irScalarBuilder) parseFieldsOf(t *ast.Call, k kind) (ir.Temp, kind, bool) {
	no := func(why string) (ir.Temp, kind, bool) {
		bl.g.dc.note("ParseFields.parse_fields at " + k.nomi() + ": " + why)
		return ir.NoTemp, kindInvalid, false
	}
	if len(t.Args) != 1 {
		return no("not one operand")
	}
	texts, tk, _, ok := bl.lower(t.Args[0])
	if !ok || tk.tag != tagList || tk.comp == nil || tk.comp.parts[0] != kindString {
		return no("texts that are not a List<String>")
	}
	mk, ok := bl.g.maybeOf(k, t)
	if !ok {
		return no("no Maybe of the record")
	}
	md := mk.def
	someV, noneV := md.variant("Some"), md.variant("None")
	if someV == nil || noneV == nil {
		return no("a Maybe without Some and None")
	}
	ty := bl.g.irTypeOf(mk)
	if ty == nil {
		return no("a Maybe with no IR type")
	}
	pos := bl.g.irNodePos(t)
	slot := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: mk})
	cont := bl.f.NewBlock(pos, "fields read")
	failed := bl.f.NewBlock(pos, "a field does not parse")

	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()

	vals := make([]ir.Temp, len(k.comp.parts))
	for i, part := range k.comp.parts {
		p := ir.NewProjElem(pos, bl.f.NewTemp(), texts, i, irParamShape(kindString))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: kindString})
		r, rk, ok := bl.parseCall(t, i, p.Dst(), part)
		if !ok {
			return no("no Parse.parse for " + part.nomi())
		}
		if rk.def == nil {
			return no("a parse that answers no Result")
		}
		okV := rk.def.variant("Ok")
		if okV == nil || len(okV.payloads) != 1 || okV.payloads[0].k != part {
			return no("a parse that answers no Result of " + part.nomi())
		}
		match := bl.variantTest(t, r, rk.def, okV)
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		parsed := bl.f.NewBlock(pos, "field parsed")
		bl.b.SetTerm(ir.NewBranch(pos, match.Dst(), parsed.ID(), failed.ID()))
		bl.b = parsed
		vals[i] = bl.variantPayload(t, r, rk.def, okV)
	}
	rec := ir.NewMakeRecord(pos, bl.f.NewTemp(), k.comp.names, vals)
	bl.b.Append(rec)
	bl.side(rec.Dst(), irScalarSide{k: k})
	some, _, _, ok := bl.variantValue(t, md, someV, []ir.Temp{rec.Dst()}, false)
	if !ok {
		return no("Some does not build")
	}
	bl.b.Append(ir.NewCopy(pos, slot, some))
	bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = failed
	none, _, _, ok := bl.variantValue(t, md, noneV, nil, true)
	if !ok {
		return no("None does not build")
	}
	bl.b.Append(ir.NewCopy(pos, slot, none))
	bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = cont
	return slot, mk, true
}

// parseCall is `F.parse(text)` for the field kind part: a program type's
// impl, or std's for a std type or a scalar.
func (bl *irScalarBuilder) parseCall(t *ast.Call, i int, text ir.Temp, part kind) (ir.Temp, kind, bool) {
	name := "%parse" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(i)
	bl.patternBinding(t, name, text, kindString)
	line := t.Line
	call := &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: part.nomi(), Line: line},
			Field:  &ast.Ident{Name: "parse", Line: line},
			Line:   line,
		},
		Args: []ast.Node{&ast.Ident{Name: name, Line: line}},
		Line: line,
	}
	if irKindDeclared(part) {
		v, rk, _, ok := bl.kindImplCall(call, part, "parse")
		return v, rk, ok
	}
	owner := part.nomi()
	if part.tag == tagNamed && part.def != nil {
		owner = part.def.nomi
	}
	if bl.g.std == nil {
		return ir.NoTemp, kindInvalid, false
	}
	var f *stdFunc
	for _, c := range bl.g.std.byType[owner+".parse"] {
		if c.iface != "Parse" {
			continue
		}
		if f != nil {
			return ir.NoTemp, kindInvalid, false
		}
		f = c
	}
	if f == nil {
		// The impl is in a std module lowered with the program, as
		// std/parse's own for Int and String are: the type-qualified call
		// reaches it as one the program wrote.
		if v, rk, _, ok, handled := bl.stdKindQualCall(call, part, "parse"); handled {
			return v, rk, ok
		}
		v, rk, _, ok := bl.lower(call)
		return v, rk, ok
	}
	args := bl.irQualLowerArgs(call)
	plan := bl.stdFuncPlan(call, args, f)
	if plan == nil {
		return ir.NoTemp, kindInvalid, false
	}
	v, rk, _, ok := bl.qualEmit(call, args, plan)
	return v, rk, ok
}

// unitParseFields is `Unit.parse_fields(texts)`: `Some(Unit)`, whatever
// the texts, which Unit has none of.
func (bl *irScalarBuilder) unitParseFields(t *ast.Call) (ir.Temp, kind, bool) {
	if len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false
	}
	if _, _, _, ok := bl.lower(t.Args[0]); !ok {
		return ir.NoTemp, kindInvalid, false
	}
	mk, ok := bl.g.maybeOf(kindUnit, t)
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	someV := mk.def.variant("Some")
	if someV == nil {
		return ir.NoTemp, kindInvalid, false
	}
	u := ir.NewUnit(bl.g.irNodePos(t), bl.f.NewTemp())
	bl.b.Append(u)
	bl.side(u.Dst(), irScalarSide{k: kindUnit})
	v, _, _, ok := bl.variantValue(t, mk.def, someV, []ir.Temp{u.Dst()}, true)
	return v, mk, ok
}
