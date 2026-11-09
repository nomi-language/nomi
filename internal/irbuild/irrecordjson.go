package irbuild

import (
	"maps"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// std/json's `ToJson` and `FromJson` at an anonymous struct (spec §12
// *Tuples, anonymous structs and functions*). No impl can be written for an
// anonymous struct, so its two functions are lowered here, as
// `Hashable.hash` over a record is (irtuplehash.go), and they answer what
// `derive ToJson` and `derive FromJson` answer for a named struct with the
// same fields and no options:
//
//   - `ToJson.to_json(r)` is `Json.Obj` of each field's `ToJson.to_json`,
//     keyed by the field's name as written; a `None` field is `null`;
//   - `R.from_json(json)` reads a `Json.Obj`: each field from its key by its
//     type's `FromJson.from_json`, a missing key of a `Maybe` field as
//     `None`, and any other missing key as the error `<key>: expected <T>,
//     got missing`. A field that does not read fails with its error, its
//     key put before the error's path. Keys the record has no field for are
//     ignored. JSON that is not an object fails as `Map<String,
//     Json>.from_json` fails, with `expected object, got <kind>`.
//
// A field is read and written in the canonical (sorted) order a record's
// kind keeps its fields in, so the first missing or failing field in that
// order is the one an error names.
//
// The record's value is reached three ways, each of which lands here: a
// direct `ToJson.to_json(r)` (typeQualCall), a bounded `T.from_json(json)`
// or `T.to_json(r)` with T bound to the record (stdKindQualCall), and the
// dispatch entry of a record boxed as a `ToJson` value (buildStdProgThunk),
// which `Json.encode_as(.{...})` and `Response.json(.{...})` reach. Each
// field's call is lowered as the call a derive writes, `ToJson.to_json(f)`
// or `F.from_json(j)`, over a name no program can spell bound to the
// field, so a field of any type reaches whatever that call reaches; a field
// that is itself a record comes back here.
//
// The checker admits the record only when each field implements the
// interface (analysis/structural_bounds.go).

// recordJsonCallArg reports whether t, a call `ToJson.to_json(x)`, has a
// record operand: by the checked parameter, or, for a call this file
// synthesized, by the kind its bound operand name holds.
func (bl *irScalarBuilder) recordJsonCallArg(t *ast.Call) bool {
	if len(t.Args) != 1 || namedArgNode(t.Args) != nil {
		return false
	}
	if ft := bl.g.checkedCallSignature(t); ft != nil {
		return len(ft.Params) == 1 && irRetainedRecordKind(bl.g.project(ft.Params[0]))
	}
	id, isIdent := t.Args[0].(*ast.Ident)
	if !isIdent {
		return false
	}
	k, bound := bl.boundK[id.Name]
	return bound && irRetainedRecordKind(k)
}

// recordToJsonCall lowers `ToJson.to_json(x)` when x is a record. handled
// is false when it is not.
func (bl *irScalarBuilder) recordToJsonCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if !bl.recordJsonCallArg(t) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	jsonK := kindInvalid
	if ft := bl.g.checkedCallSignature(t); ft != nil {
		jsonK = bl.g.project(ft.Return)
	}
	subj, sk, _, sok := bl.lower(t.Args[0])
	if !sok || !irRetainedRecordKind(sk) {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	bl.noteQualRecordRow(t, subj, sk)
	v, k, ok = bl.recordToJsonOf(t, subj, sk, jsonK, "")
	return v, k, false, ok, true
}

// recordJsonKindCall lowers `T.to_json(r)` or `T.from_json(json)` with T
// bound to the record kind k. handled is false for any other call.
func (bl *irScalarBuilder) recordJsonKindCall(t *ast.Call, k kind, method string) (ir.Temp, kind, bool, bool, bool) {
	if !irRetainedRecordKind(k) || len(t.Args) != 1 || namedArgNode(t.Args) != nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch iface := bl.boundCallIface(fa); {
	case method == "to_json" && (iface == "ToJson" || iface == ""):
		jsonK := kindInvalid
		if ft := bl.g.checkedCallSignature(t); ft != nil {
			jsonK = bl.g.project(ft.Return)
		}
		subj, sk, _, ok := bl.lower(t.Args[0])
		if !ok || sk != k {
			return ir.NoTemp, kindInvalid, false, false, true
		}
		bl.noteQualRecordRow(t, subj, sk)
		v, rk, ok := bl.recordToJsonOf(t, subj, k, jsonK, "")
		return v, rk, false, ok, true
	case method == "from_json" && (iface == "FromJson" || iface == ""):
		resultK := kindInvalid
		if ft := bl.g.checkedCallSignature(t); ft != nil {
			resultK = bl.g.project(ft.Return)
		}
		json, jk, _, ok := bl.lower(t.Args[0])
		if !ok {
			return ir.NoTemp, kindInvalid, false, false, true
		}
		bl.noteQualRecordRow(t, json, jk)
		v, rk, ok := bl.recordFromJsonOf(t, json, jk, k, resultK, "")
		return v, rk, false, ok, true
	}
	return ir.NoTemp, kindInvalid, false, false, false
}

// noteQualRecordRow records v, the call's one operand, as its row when the
// call is an assertion's subject, as tupleHashOf does.
func (bl *irScalarBuilder) noteQualRecordRow(t *ast.Call, v ir.Temp, k kind) {
	if bl.qualRecord == t {
		bl.qualRecordArgs = irQualArgs{temps: []ir.Temp{v}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
		bl.qualRecordSeen = true
	}
}

// recordToJsonOf is `Json.Obj` of subj's fields' JSON, for subj a lowered
// record of kind sk. jsonK is the call's Json kind when the caller knows
// it, else kindInvalid, and the first field's answer names it. path tells
// a nested record's synthesized names from its parent's.
func (bl *irScalarBuilder) recordToJsonOf(t *ast.Call, subj ir.Temp, sk kind, jsonK kind, path string) (ir.Temp, kind, bool) {
	no := func(why string) (ir.Temp, kind, bool) {
		bl.g.dc.note("ToJson.to_json at " + sk.nomi() + ": " + why)
		return ir.NoTemp, kindInvalid, false
	}
	if sk.comp == nil || len(sk.comp.names) != len(sk.comp.parts) {
		return no("no fields")
	}
	recording, qualRecord := bl.recording, bl.qualRecord
	bl.recording, bl.qualRecord = 0, nil
	defer func() { bl.recording, bl.qualRecord = recording, qualRecord }()
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()

	pos := bl.g.irNodePos(t)
	kvs := make([]ir.Temp, 0, 2*len(sk.comp.parts))
	for i, part := range sk.comp.parts {
		name := sk.comp.names[i]
		field, _, _, ok := bl.recordRead(t, name, subj, sk, true)
		if !ok {
			return no("field " + name + " does not read")
		}
		var jv ir.Temp
		var jk kind
		switch {
		case irRetainedRecordKind(part):
			jv, jk, ok = bl.recordToJsonOf(t, field, part, jsonK, path+"."+strconv.Itoa(i))
		// kindInvalid: sentinel — no Json kind is known yet; the field's own ToJson call below names it.
		case irUnitMaybe(part) && jsonK != kindInvalid:
			// `.{detail: None}`: a None nothing typed, read as a Maybe of
			// Unit, which no ToJson impl answers. It is always None.
			jv, jk, ok = bl.jsonNull(t, jsonK)
		default:
			jv, jk, ok = bl.lowerJsonFieldCall(t, field, part, path+"."+strconv.Itoa(i), func(arg ast.Node) *ast.Call {
				return synthIfaceCall(t.Line, "ToJson", "to_json", arg)
			})
		}
		if !ok {
			return no("field " + name + "'s ToJson.to_json")
		}
		// kindInvalid: sentinel — the caller passed no Json kind, so the first field's answer names it.
		if jsonK == kindInvalid {
			jsonK = jk
		}
		if jk != jsonK {
			return no("field " + name + "'s JSON is a " + jk.nomi())
		}
		key := ir.NewString(pos, bl.f.NewTemp(), name)
		bl.b.Append(key)
		bl.side(key.Dst(), irScalarSide{k: kindString})
		kvs = append(kvs, key.Dst(), jv)
	}
	if jsonK.tag != tagNamed || jsonK.def == nil {
		return no("no Json kind")
	}
	objV := jsonK.def.variant("Obj")
	if objV == nil || len(objV.payloads) != 1 {
		return no("a Json without Obj")
	}
	mk := objV.payloads[0].k
	if mk.tag != tagMap || mk.comp == nil || mk.comp.parts[0] != kindString || mk.comp.parts[1] != jsonK {
		return no("a Json.Obj that holds no Map<String, Json>")
	}
	m := ir.NewMakeMap(pos, bl.f.NewTemp(), kvs)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: mk})
	v, rk, _, ok := bl.variantValue(t, jsonK.def, objV, []ir.Temp{m.Dst()}, false)
	if !ok {
		return no("Json.Obj does not build")
	}
	return v, rk, true
}

// irUnitMaybe reports whether k is `Maybe<Unit>`, the kind of a bare
// `None` whose element nothing determines.
func irUnitMaybe(k kind) bool {
	return irMaybeKind(k) && len(k.def.preludeArgs) == 1 && k.def.preludeArgs[0] == kindUnit
}

// jsonNull is `Json.Null` of the Json kind jsonK.
func (bl *irScalarBuilder) jsonNull(t *ast.Call, jsonK kind) (ir.Temp, kind, bool) {
	if jsonK.tag != tagNamed || jsonK.def == nil {
		return ir.NoTemp, kindInvalid, false
	}
	null := jsonK.def.variant("Null")
	if null == nil {
		return ir.NoTemp, kindInvalid, false
	}
	v, k, _, ok := bl.variantValue(t, jsonK.def, null, nil, true)
	return v, k, ok
}

// synthIfaceCall is `Owner.method(arg)` on line, at column 0, where the
// checker records nothing.
func synthIfaceCall(line int, owner, method string, arg ast.Node) *ast.Call {
	return &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: owner, Line: line},
			Field:  &ast.Ident{Name: method, Line: line},
			Line:   line,
		},
		Args: []ast.Node{arg},
		Line: line,
	}
}

// lowerJsonFieldCall binds v, a field's value of kind part, to a name no
// program can spell and lowers the call call builds over that name.
func (bl *irScalarBuilder) lowerJsonFieldCall(t *ast.Call, v ir.Temp, part kind, path string, call func(ast.Node) *ast.Call) (ir.Temp, kind, bool) {
	name := "%json" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + path
	bl.patternBinding(t, name, v, part)
	r, rk, _, ok := bl.lower(call(&ast.Ident{Name: name, Line: t.Line}))
	return r, rk, ok
}

// recordFromJsonOf is `R.from_json(json)` for R the record kind k, json a
// lowered value of kind jk. resultK is the call's `Result<R,
// Json.ShapeError>` kind when the caller knows it, else kindInvalid.
func (bl *irScalarBuilder) recordFromJsonOf(t *ast.Call, json ir.Temp, jk kind, k kind, resultK kind, path string) (ir.Temp, kind, bool) {
	no := func(why string) (ir.Temp, kind, bool) {
		bl.g.dc.note("FromJson.from_json at " + k.nomi() + ": " + why)
		return ir.NoTemp, kindInvalid, false
	}
	if k.comp == nil || len(k.comp.names) != len(k.comp.parts) {
		return no("no fields")
	}
	recording, qualRecord := bl.recording, bl.qualRecord
	bl.recording, bl.qualRecord = 0, nil
	defer func() { bl.recording, bl.qualRecord = recording, qualRecord }()
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()

	pos := bl.g.irNodePos(t)
	if jk.tag != tagNamed || jk.def == nil {
		return no("JSON of kind " + jk.nomi())
	}
	objV := jk.def.variant("Obj")
	if objV == nil || len(objV.payloads) != 1 {
		return no("a Json without Obj")
	}
	mk := objV.payloads[0].k

	// The object's fields, or the error a JSON value that is no object
	// gets: `Map<String, Json>.from_json`'s own.
	fields, fk, ok := bl.fromJsonAt(t, json, jk, mk, path+".obj")
	if !ok {
		return no("no Map<String, Json>.from_json")
	}
	fd := fk.def
	if fk.tag != tagNamed || fd == nil {
		return no("a Map<String, Json>.from_json that answers no Result")
	}
	fOk, fErr := fd.variant("Ok"), fd.variant("Err")
	if fOk == nil || fErr == nil || len(fOk.payloads) != 1 || len(fErr.payloads) != 1 || fOk.payloads[0].k != mk {
		return no("a Map<String, Json>.from_json that answers no Result of the map")
	}
	shapeK := fErr.payloads[0].k
	// kindInvalid: sentinel — the caller passed no result kind, so it is built from k and the shape error.
	if resultK == kindInvalid {
		anchor, _ := bl.g.preludeNamed("Result")
		if anchor == nil {
			return no("no prelude Result")
		}
		resultK = bl.g.preludeInstanceOf(anchor.spec, []kind{k, shapeK})
	}
	rd := resultK.def
	if resultK.tag != tagNamed || rd == nil {
		return no("a result of kind " + resultK.nomi())
	}
	rOk, rErr := rd.variant("Ok"), rd.variant("Err")
	if rOk == nil || rErr == nil || len(rOk.payloads) != 1 || len(rErr.payloads) != 1 || rOk.payloads[0].k != k || rErr.payloads[0].k != shapeK {
		return no("a result that is no Result<" + k.nomi() + ", Json.ShapeError>")
	}
	ty := bl.g.irTypeOf(resultK)
	if ty == nil {
		return no("a Result with no IR type")
	}
	slot := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: resultK})
	cont := bl.f.NewBlock(pos, "record read")
	failed := bl.f.NewBlock(pos, "record not read")

	// failWith ends the current block with Err(e) in the slot.
	failWith := func(e ir.Temp) bool {
		errV, _, _, ok := bl.variantValue(t, rd, rErr, []ir.Temp{e}, false)
		if !ok {
			return false
		}
		bl.b.Append(ir.NewCopy(pos, slot, errV))
		bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
		return true
	}

	isObj := bl.variantTest(t, fields, fd, fOk)
	bl.b.Append(isObj)
	bl.side(isObj.Dst(), irScalarSide{k: kindBool})
	obj := bl.f.NewBlock(pos, "object")
	bl.b.SetTerm(ir.NewBranch(pos, isObj.Dst(), obj.ID(), failed.ID()))
	bl.b = failed
	if !failWith(bl.variantPayload(t, fields, fd, fErr)) {
		return no("Err does not build")
	}
	bl.b = obj
	m := bl.variantPayload(t, fields, fd, fOk)

	vals := make([]ir.Temp, len(k.comp.parts))
	for i, part := range k.comp.parts {
		name := k.comp.names[i]
		fpath := path + "." + strconv.Itoa(i)
		key := bl.stringConst(pos, name)
		got, gk, ok := bl.mapGetAt(t, m, mk, key)
		if !ok {
			return no("no Map.get")
		}
		md := gk.def
		mSome, mNone := md.variant("Some"), md.variant("None")
		if mSome == nil || mNone == nil {
			return no("a Maybe without Some and None")
		}
		vt := bl.g.irTypeOf(part)
		if vt == nil {
			return no("a field of kind " + part.nomi() + " with no IR type")
		}
		val := bl.f.NewTemp()
		bl.b.Append(ir.NewSlot(pos, val, vt))
		bl.side(val, irScalarSide{k: part})
		next := bl.f.NewBlock(pos, "field read")
		present := bl.f.NewBlock(pos, "field present")
		missing := bl.f.NewBlock(pos, "field missing")
		has := bl.variantTest(t, got, md, mSome)
		bl.b.Append(has)
		bl.side(has.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, has.Dst(), present.ID(), missing.ID()))

		// A missing key: None for a Maybe field, else the error naming it.
		bl.b = missing
		if irMaybeKind(part) {
			pd := part.def
			none := pd.variant("None")
			if none == nil {
				return no("a Maybe field without None")
			}
			nv, _, _, ok := bl.variantValue(t, pd, none, nil, true)
			if !ok {
				return no("None does not build")
			}
			bl.b.Append(ir.NewCopy(pos, val, nv))
			bl.b.SetTerm(ir.NewJump(pos, next.ID()))
		} else {
			e, ok := bl.shapeErrorAt(t, shapeK, []ir.Temp{bl.stringConst(pos, name)}, bl.stringConst(pos, part.nomi()), bl.stringConst(pos, "missing"))
			if !ok || !failWith(e) {
				return no("the missing field's error does not build")
			}
		}

		// A present key: the field's type reads it.
		bl.b = present
		jv := bl.variantPayload(t, got, md, mSome)
		r, rk, ok := bl.fromJsonAt(t, jv, jk, part, fpath)
		if !ok {
			return no("no " + part.nomi() + ".from_json")
		}
		pd := rk.def
		if rk.tag != tagNamed || pd == nil {
			return no("a from_json that answers no Result")
		}
		pOk, pErr := pd.variant("Ok"), pd.variant("Err")
		if pOk == nil || pErr == nil || len(pOk.payloads) != 1 || pOk.payloads[0].k != part || len(pErr.payloads) != 1 || pErr.payloads[0].k != shapeK {
			return no("a from_json that answers no Result<" + part.nomi() + ", Json.ShapeError>")
		}
		read := bl.variantTest(t, r, pd, pOk)
		bl.b.Append(read)
		bl.side(read.Dst(), irScalarSide{k: kindBool})
		readOK := bl.f.NewBlock(pos, "field value read")
		readErr := bl.f.NewBlock(pos, "field value not read")
		bl.b.SetTerm(ir.NewBranch(pos, read.Dst(), readOK.ID(), readErr.ID()))
		bl.b = readErr
		e, ok := bl.shapeErrorPrepend(t, shapeK, bl.variantPayload(t, r, pd, pErr), name)
		if !ok || !failWith(e) {
			return no("the field's error does not build")
		}
		bl.b = readOK
		bl.b.Append(ir.NewCopy(pos, val, bl.variantPayload(t, r, pd, pOk)))
		bl.b.SetTerm(ir.NewJump(pos, next.ID()))

		bl.b = next
		vals[i] = val
	}
	rec := ir.NewMakeRecord(pos, bl.f.NewTemp(), k.comp.names, vals)
	bl.b.Append(rec)
	bl.side(rec.Dst(), irScalarSide{k: k})
	okV, _, _, ok := bl.variantValue(t, rd, rOk, []ir.Temp{rec.Dst()}, false)
	if !ok {
		return no("Ok does not build")
	}
	bl.b.Append(ir.NewCopy(pos, slot, okV))
	bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = cont
	return slot, resultK, true
}

// fromJsonAt is `F.from_json(json)` for the field kind part: a record's
// here, and any other type's as the call a derive writes reaches it.
func (bl *irScalarBuilder) fromJsonAt(t *ast.Call, json ir.Temp, jk kind, part kind, path string) (ir.Temp, kind, bool) {
	if irRetainedRecordKind(part) {
		return bl.recordFromJsonOf(t, json, jk, part, kindInvalid, path)
	}
	name := "%json" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + path
	bl.patternBinding(t, name, json, jk)
	line := t.Line
	owner := part.nomi()
	if part.tag == tagNamed && part.def != nil {
		owner = part.def.nomi
	}
	call := synthIfaceCall(line, owner, "from_json", &ast.Ident{Name: name, Line: line})
	if irKindDeclared(part) {
		v, rk, _, ok := bl.kindImplCall(call, part, "from_json")
		return v, rk, ok
	}
	if bl.g.std != nil {
		var f *stdFunc
		for _, c := range bl.g.std.byType[owner+".from_json"] {
			if c.iface != "FromJson" {
				continue
			}
			if f != nil {
				return ir.NoTemp, kindInvalid, false
			}
			f = c
		}
		if f != nil {
			args := bl.irQualLowerArgs(call)
			plan := bl.stdFuncPlan(call, args, f)
			if plan == nil {
				return ir.NoTemp, kindInvalid, false
			}
			v, rk, _, ok := bl.qualEmit(call, args, plan)
			return v, rk, ok
		}
	}
	if v, rk, _, ok, handled := bl.stdKindQualCall(call, part, "from_json"); handled {
		return v, rk, ok
	}
	v, rk, _, ok := bl.lower(call)
	return v, rk, ok
}

// hostOperandsCall is a call at t's position with n operands, standing for
// a host call this file emits over operands it has lowered already: the
// plans read a call's operands only for their positions.
func hostOperandsCall(t *ast.Call, n int) *ast.Call {
	args := make([]ast.Node, n)
	for i := range args {
		args[i] = &ast.Ident{Name: "_", Line: t.Line, Col: t.Col}
	}
	return &ast.Call{Func: t.Func, Args: args, Line: t.Line, Col: t.Col}
}

// stringConst is the String s.
func (bl *irScalarBuilder) stringConst(pos ir.Pos, s string) ir.Temp {
	c := ir.NewString(pos, bl.f.NewTemp(), s)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindString})
	return c.Dst()
}

// mapGetAt is `Map.get(m, key)` for m of kind mk.
func (bl *irScalarBuilder) mapGetAt(t *ast.Call, m ir.Temp, mk kind, key ir.Temp) (ir.Temp, kind, bool) {
	args := irQualArgs{temps: []ir.Temp{m, key}, kinds: []kind{mk, kindString}, mobile: []bool{true, true}, ok: true}
	call := hostOperandsCall(t, 2)
	plan := bl.mapCallPlan(call, args, "get")
	if plan == nil {
		return ir.NoTemp, kindInvalid, false
	}
	v, rk, _, ok := bl.qualEmit(call, args, plan)
	if !ok || rk.tag != tagNamed || rk.def == nil {
		return ir.NoTemp, kindInvalid, false
	}
	return v, rk, true
}

// shapeErrorAt builds `Json.ShapeError{path, expected, got}`, path the
// List<String> of the segments.
func (bl *irScalarBuilder) shapeErrorAt(t *ast.Call, shapeK kind, path []ir.Temp, expected, got ir.Temp) (ir.Temp, bool) {
	d := shapeK.def
	if shapeK.tag != tagNamed || d == nil || len(d.fields) != 3 {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(t)
	lk := bl.g.listKind(kindString)
	list := ir.NewMakeList(pos, bl.f.NewTemp(), path, ir.NoTemp)
	bl.b.Append(list)
	bl.side(list.Dst(), irScalarSide{k: lk})
	return bl.shapeErrorOf(t, d, map[string]ir.Temp{"path": list.Dst(), "expected": expected, "got": got})
}

// shapeErrorOf is the ShapeError struct d with the field values vals.
func (bl *irScalarBuilder) shapeErrorOf(t *ast.Call, d *typeDef, vals map[string]ir.Temp) (ir.Temp, bool) {
	ops := make([]ir.Temp, len(d.fields))
	for i, f := range d.fields {
		v, found := vals[f.nomi]
		if !found {
			return ir.NoTemp, false
		}
		ops[i] = v
	}
	m := ir.NewMakeStruct(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irTypeSym(d), irStructFieldNames(d), ops)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: named(d)})
	return m.Dst(), true
}

// shapeErrorPrepend is std/json's `shape_error_prepend(e, segment)`: e
// with segment before its path.
func (bl *irScalarBuilder) shapeErrorPrepend(t *ast.Call, shapeK kind, e ir.Temp, segment string) (ir.Temp, bool) {
	d := shapeK.def
	if shapeK.tag != tagNamed || d == nil || len(d.fields) != 3 {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(t)
	vals := map[string]ir.Temp{}
	for _, f := range d.fields {
		p := ir.NewProjField(pos, bl.f.NewTemp(), e, bl.g.irTypes().Symbol(f, f.nomi), f.nomi, irParamShape(f.k))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: f.k})
		vals[f.nomi] = p.Dst()
	}
	lk := bl.g.listKind(kindString)
	head := ir.NewMakeList(pos, bl.f.NewTemp(), []ir.Temp{bl.stringConst(pos, segment)}, ir.NoTemp)
	bl.b.Append(head)
	bl.side(head.Dst(), irScalarSide{k: lk})
	args := irQualArgs{temps: []ir.Temp{head.Dst(), vals["path"]}, kinds: []kind{lk, lk}, mobile: []bool{true, true}, ok: true}
	call := hostOperandsCall(t, 2)
	plan := bl.listCallPlan(call, args, "concat")
	if plan == nil {
		return ir.NoTemp, false
	}
	joined, jk, _, ok := bl.qualEmit(call, args, plan)
	if !ok || jk != lk {
		return ir.NoTemp, false
	}
	vals["path"] = joined
	return bl.shapeErrorOf(t, d, vals)
}
