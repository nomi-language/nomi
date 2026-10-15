package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedRecordKind(k kind) bool {
	if k.tag != tagAnonStruct || k.comp == nil || len(k.comp.names) == 0 || len(k.comp.names) != len(k.comp.parts) {
		return false
	}
	for _, part := range k.comp.parts {
		if !irCompositePartKind(part) {
			return false
		}
	}
	return true
}

func (bl *irScalarBuilder) recordMake(t *ast.StructLit) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no expected record type.
	return bl.recordMakeOf(t, kindInvalid)
}

// recordMakeOf builds an anonymous record literal; want, when it is a record
// type with the literal's field names, gives each field the kind its value
// is lowered against (`{a: []}` where a `{a: List<Int>}` is expected).
func (bl *irScalarBuilder) recordMakeOf(t *ast.StructLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.Spread != nil {
		return bl.structSpread(t)
	}
	if t.Spread != nil || len(t.Fields) == 0 {
		return no()
	}
	names := make([]string, len(t.Fields))
	parts := make([]kind, len(t.Fields))
	values := make(map[string]ir.Temp, len(t.Fields))
	pure := true
	for i, field := range t.Fields {
		var v ir.Temp
		var k kind
		var mobile, ok bool
		if fk, typed := irRecordField(want, field.Name, len(t.Fields)); typed {
			v, k, mobile, ok = bl.lowerWant(field.Value, fk)
		} else {
			v, k, mobile, ok = bl.lower(field.Value)
		}
		if !ok || !irCompositePartKind(k) {
			return no()
		}
		if !mobile && i != len(t.Fields)-1 {
			mobile = true
		}
		names[i], parts[i], values[field.Name] = field.Name, k, v
		pure = pure && mobile
	}
	k := bl.g.anonStructKind(names, parts)
	if !irRetainedRecordKind(k) {
		return no()
	}
	ops := make([]ir.Temp, len(k.comp.names))
	for i, name := range k.comp.names {
		ops[i] = values[name]
	}
	n := ir.NewMakeRecord(bl.g.irNodePos(t), bl.f.NewTemp(), k.comp.names, ops)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k, pureMake: pure})
	return n.Dst(), k, pure, true
}

// irRecordField is the kind of field name in record type want, when want is
// a record of n fields that has it.
func irRecordField(want kind, name string, n int) (kind, bool) {
	if want.tag != tagAnonStruct || want.comp == nil || len(want.comp.names) != n || len(want.comp.parts) != n {
		return kindInvalid, false
	}
	for i, have := range want.comp.names {
		if have == name {
			return want.comp.parts[i], true
		}
	}
	return kindInvalid, false
}

func (bl *irScalarBuilder) recordRead(at ast.Node, name string, subj ir.Temp, k kind, mobile bool) (ir.Temp, kind, bool, bool) {
	part, found := anonFieldKind(k, name)
	if !found {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewProjRecordField(bl.g.irNodePos(at), bl.f.NewTemp(), subj, name, irParamShape(part))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: part})
	return n.Dst(), part, mobile, true
}

// structSpread lowers `{..base, f: v}` as structSpreadLit does: the base is
// forced, then the fields merge as structMerge does.
func (bl *irScalarBuilder) structSpread(t *ast.StructLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if c, missing := bl.currentMissing(t.Spread); missing {
		// `{.., f: v}` where the position holds no value: build it.
		return bl.updateBuild(t, c.k)
	}
	base, k, _, ok := bl.lower(t.Spread)
	if !ok || !irRetainedUpdateKind(k) {
		return no()
	}
	if len(t.Fields) == 0 {
		// A copy with no changes is the base itself, answered impure.
		return base, k, false, true
	}
	v, ok := bl.structMerge(t, k, base, t)
	if !ok {
		return no()
	}
	return v, k, false, true
}

// irRetainedUpdateKind is a base a functional update copies: a retained record
// or declared struct.
func irRetainedUpdateKind(k kind) bool {
	return irRetainedRecordKind(k) || (k.tag == tagNamed && irRetainedStructKind(k.def))
}

// structMerge mirrors gen.structMerge over a lowered base: every value but the
// last is forced, and each is coerced to its field's kind. A field written as
// an update literal (`address: {.., city: "NYC"}`) has the field projected
// from this base as its `..` (ircurrentvalue.go). pre, when not nil, holds
// the values of the fields already lowered, by index (structUpdateHoles).
func (bl *irScalarBuilder) structMerge(at ast.Node, k kind, base ir.Temp, lit *ast.StructLit) (ir.Temp, bool) {
	return bl.structMergePre(at, k, base, lit, nil)
}

func (bl *irScalarBuilder) structMergePre(at ast.Node, k kind, base ir.Temp, lit *ast.StructLit, pre map[int]irLowered) (ir.Temp, bool) {
	names, vals, ok := bl.structMergeVals(k, base, lit, pre)
	if !ok {
		return ir.NoTemp, false
	}
	n := ir.NewMakeUpdate(bl.g.irNodePos(at), bl.f.NewTemp(), base, names, vals)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), true
}

// irLowered is one value already lowered.
type irLowered struct {
	v ir.Temp
	k kind
}

// structMergeVals lowers the fields of lit, an update of base of kind k, and
// answers each one's name and value.
func (bl *irScalarBuilder) structMergeVals(k kind, base ir.Temp, lit *ast.StructLit, pre map[int]irLowered) ([]string, []ir.Temp, bool) {
	names := make([]string, len(lit.Fields))
	vals := make([]ir.Temp, len(lit.Fields))
	for i, f := range lit.Fields {
		fk, found := bl.g.updateField(k, f.Name)
		if !found {
			return nil, nil, false
		}
		if ast.UpdateHole(f.Value) != nil {
			sub, ok := bl.updateBaseField(f.Value, k, base, f.Name, fk)
			if !ok {
				return nil, nil, false
			}
			bl.setCurrent(f.Value, irCurrent{v: sub, k: fk})
		}
		var v ir.Temp
		var vk kind
		ok := true
		if p, done := pre[i]; done {
			v, vk = p.v, p.k
		} else {
			v, vk, _, ok = bl.lower(f.Value)
		}
		if !ok {
			return nil, nil, false
		}
		if vk != fk && bl.g.irErases(fk, vk) {
			// A concrete implementer at an interface-typed field, admitted
			// as the literal admits it; see structMakeOf.
			names[i], vals[i] = f.Name, v
			continue
		}
		v, vk, ok = bl.coerceEmpty(f.Value, v, vk, fk)
		if !ok || vk != fk {
			return nil, nil, false
		}
		names[i], vals[i] = f.Name, v
	}
	return names, vals, true
}

// updateBaseField projects the field a nested update starts from.
func (bl *irScalarBuilder) updateBaseField(at ast.Node, k kind, base ir.Temp, name string, fk kind) (ir.Temp, bool) {
	if k.tag == tagAnonStruct {
		p := ir.NewProjRecordField(bl.g.irNodePos(at), bl.f.NewTemp(), base, name, irParamShape(fk))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: fk})
		return p.Dst(), true
	}
	f := k.def.field(name)
	if f == nil {
		return ir.NoTemp, false
	}
	p := ir.NewProjField(bl.g.irNodePos(at), bl.f.NewTemp(), base,
		bl.g.irTypes().Symbol(f, name), name, irParamShape(fk))
	bl.b.Append(p)
	bl.side(p.Dst(), irScalarSide{k: fk})
	return p.Dst(), true
}

// structUpdateCall lowers `Struct.update(base, {f: v})`: std's own `Struct`,
// a bare anonymous patch, the base forced, then the same merge a spread uses.
// A `Struct` a type parameter or local interface names declines here. Its
// arguments may be named by `Struct`'s parameters
// (`Struct.update(updates: {x: 1}, original: p)`); they are evaluated in
// Nomi's order, positionals first and then named arguments as written.
func (bl *irScalarBuilder) structUpdateCall(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Args) != 2 || !bl.g.stdStructUpdateIsStds() {
		return no()
	}
	if _, local := bl.g.ifaceNamed("Struct"); local {
		return no()
	}
	ops, ok := structUpdateOperands(t.Args)
	if !ok {
		return no()
	}
	lit, isLit := argExpr(t.Args[ops.patch]).(*ast.StructLit)
	if !isLit {
		return bl.structUpdateValue(t, ops)
	}
	if lit.TypeName != nil || lit.Spread != nil {
		return no()
	}
	recorded := bl.recording > 0 && bl.qualRecord == t
	if (ops.patchFirst || recorded) && patchHasHole(lit) {
		return bl.structUpdateHoles(t, ops, lit, recorded)
	}
	if ops.patchFirst || recorded {
		// The patch is built once as a record and the update reads each
		// field back from it: inside an assertion subject the record is the
		// patch's `values:` row, and a patch evaluated before the base must
		// be built before the base is.
		var rec, base ir.Temp
		var rk, k kind
		if ops.patchFirst {
			if rec, rk, _, ok = bl.recordMake(lit); !ok {
				return no()
			}
		}
		if base, k, _, ok = bl.lower(argExpr(t.Args[ops.base])); !ok || !irRetainedUpdateKind(k) {
			return no()
		}
		if !ops.patchFirst {
			if rec, rk, _, ok = bl.recordMake(lit); !ok {
				return no()
			}
		}
		fields, from := make([]string, len(lit.Fields)), make([]ast.Node, len(lit.Fields))
		for i, f := range lit.Fields {
			fields[i], from[i] = f.Name, f.Value
		}
		v, ok := bl.recordMerge(t, k, base, rec, rk, fields, from)
		if !ok {
			return no()
		}
		if recorded {
			temps, kinds := make([]ir.Temp, 2), make([]kind, 2)
			temps[ops.base], kinds[ops.base] = base, k
			temps[ops.patch], kinds[ops.patch] = rec, rk
			bl.qualRecordArgs = irQualArgs{temps: temps, kinds: kinds, mobile: []bool{true, true}, ok: true}
			bl.qualRecordSeen = true
		}
		return v, k, false, true
	}
	base, k, _, ok := bl.lower(argExpr(t.Args[ops.base]))
	if !ok || !irRetainedUpdateKind(k) {
		return no()
	}
	if len(lit.Fields) == 0 {
		return base, k, false, true
	}
	v, ok := bl.structMerge(t, k, base, lit)
	if !ok {
		return no()
	}
	return v, k, false, true
}

// structUpdateValue lowers `Struct.update(base, patch)` whose patch is a
// value of an anonymous struct type rather than a literal: a binding, a
// call, a field. The operands are evaluated in Nomi's order, and the update
// writes every field the patch's type names, read back from the patch.
func (bl *irScalarBuilder) structUpdateValue(t *ast.Call, ops structUpdateArgs) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	temps, kinds := make([]ir.Temp, 2), make([]kind, 2)
	order := []int{ops.base, ops.patch}
	if ops.patchFirst {
		order = []int{ops.patch, ops.base}
	}
	for _, i := range order {
		v, k, _, ok := bl.lower(argExpr(t.Args[i]))
		if !ok {
			return no()
		}
		temps[i], kinds[i] = v, k
	}
	base, k := temps[ops.base], kinds[ops.base]
	rec, rk := temps[ops.patch], kinds[ops.patch]
	if !irRetainedUpdateKind(k) || rk.tag != tagAnonStruct || rk.comp == nil {
		irDeclineNote("a Struct.update patch value that is not an anonymous struct: " + rk.nomi())
		return no()
	}
	patch := argExpr(t.Args[ops.patch])
	from := make([]ast.Node, len(rk.comp.names))
	for i := range from {
		from[i] = patch
	}
	v, ok := bl.recordMerge(t, k, base, rec, rk, rk.comp.names, from)
	if !ok {
		return no()
	}
	if bl.recording > 0 && bl.qualRecord == t {
		bl.qualRecordArgs = irQualArgs{temps: temps, kinds: kinds, mobile: []bool{true, true}, ok: true}
		bl.qualRecordSeen = true
	}
	return v, k, false, true
}

// structUpdateArgs is where `Struct.update`'s two operands are written: the
// index of the base and of the patch among the call's arguments, and whether
// the patch is evaluated first.
type structUpdateArgs struct {
	base, patch int
	patchFirst  bool
}

// structUpdateOperands places `Struct.update`'s two written arguments,
// positional or named by `Struct`'s parameters (`original`, `updates`), as
// any call places them (argSlotPlan).
func structUpdateOperands(args []ast.Node) (structUpdateArgs, bool) {
	plan, planned := argSlotPlan(args, make([]kind, 2), []string{"original", "updates"})
	if !planned || len(plan.order) != 2 {
		return structUpdateArgs{}, false
	}
	at := [2]int{-1, -1}
	for _, i := range plan.order {
		s := plan.slots[i]
		if s < 0 || s > 1 || at[s] >= 0 {
			return structUpdateArgs{}, false
		}
		at[s] = i
	}
	if at[0] < 0 || at[1] < 0 {
		return structUpdateArgs{}, false
	}
	return structUpdateArgs{base: at[0], patch: at[1], patchFirst: plan.order[0] == at[1]}, true
}

// recordMerge is structMerge over a patch already built as the record rec of
// kind rk: each field is read back from rec. fields names the fields the
// update writes, and from the node each one's value is read at: a literal
// patch's field values, or the patch itself when it is a value
// (`Struct.update(u, patch)`). A patch with a `..` field never reaches it
// (structUpdateHoles).
func (bl *irScalarBuilder) recordMerge(at ast.Node, k kind, base, rec ir.Temp, rk kind, fields []string, from []ast.Node) (ir.Temp, bool) {
	names := make([]string, len(fields))
	vals := make([]ir.Temp, len(fields))
	for i, name := range fields {
		fk, found := bl.g.updateField(k, name)
		part, inRecord := anonFieldKind(rk, name)
		if !found || !inRecord {
			return ir.NoTemp, false
		}
		p := ir.NewProjRecordField(bl.g.irNodePos(from[i]), bl.f.NewTemp(), rec, name, irParamShape(part))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: part})
		v, vk, ok := bl.coerceEmpty(from[i], p.Dst(), part, fk)
		if !ok || vk != fk {
			return ir.NoTemp, false
		}
		names[i], vals[i] = name, v
	}
	n := ir.NewMakeUpdate(bl.g.irNodePos(at), bl.f.NewTemp(), base, names, vals)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), true
}
