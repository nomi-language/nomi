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
		v, k, mobile, ok := bl.lower(field.Value)
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
// last is forced, each is coerced to its field's kind, and a bare anonymous
// literal at a field is a nested patch over that field, whose base projects
// the field from this base.
func (bl *irScalarBuilder) structMerge(at ast.Node, k kind, base ir.Temp, lit *ast.StructLit) (ir.Temp, bool) {
	names := make([]string, len(lit.Fields))
	vals := make([]ir.Temp, len(lit.Fields))
	for i, f := range lit.Fields {
		fk, found := bl.g.updateField(k, f.Name)
		if !found {
			return ir.NoTemp, false
		}
		if nested, isLit := f.Value.(*ast.StructLit); isLit && nested.TypeName == nil && nested.Spread == nil {
			// An empty patch spells the field itself, which no node here
			// names.
			if len(nested.Fields) == 0 || !irRetainedUpdateKind(fk) {
				return ir.NoTemp, false
			}
			sub, ok := bl.updateBaseField(f.Value, k, base, f.Name, fk)
			if !ok {
				return ir.NoTemp, false
			}
			v, ok := bl.structMerge(f.Value, fk, sub, nested)
			if !ok {
				return ir.NoTemp, false
			}
			names[i], vals[i] = f.Name, v
			continue
		}
		v, vk, _, ok := bl.lower(f.Value)
		if !ok {
			return ir.NoTemp, false
		}
		if vk != fk && bl.g.irErases(fk, vk) {
			// A concrete implementer at an interface-typed field, admitted
			// as the literal admits it; see structMakeOf.
			names[i], vals[i] = f.Name, v
			continue
		}
		v, vk, ok = bl.coerceEmpty(f.Value, v, vk, fk)
		if !ok || vk != fk {
			return ir.NoTemp, false
		}
		names[i], vals[i] = f.Name, v
	}
	n := ir.NewMakeUpdate(bl.g.irNodePos(at), bl.f.NewTemp(), base, names, vals)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), true
}

// updateBaseField projects the field a nested patch starts from.
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
// A `Struct` a type parameter or local interface names declines here.
func (bl *irScalarBuilder) structUpdateCall(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Args) != 2 || !bl.g.stdStructUpdateIsStds() {
		return no()
	}
	if _, local := bl.g.ifaceNamed("Struct"); local {
		return no()
	}
	if _, bound := bl.g.genericSubstKind("Struct"); bound {
		return no()
	}
	lit, isLit := t.Args[1].(*ast.StructLit)
	if !isLit || lit.TypeName != nil || lit.Spread != nil {
		return no()
	}
	base, k, _, ok := bl.lower(t.Args[0])
	if !ok || !irRetainedUpdateKind(k) {
		return no()
	}
	if bl.recording > 0 && bl.qualRecord == t {
		return bl.recordedStructUpdate(t, k, base, lit)
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

// recordedStructUpdate is structUpdateCall inside an assertion subject, where
// the call's two operands are `values:` rows: the base, and the patch as the
// record it evaluates to (`{y: 5, name: "q"}`). The patch is
// built once as that record, in source order, and the update reads each field
// back from it, so the row and the update see the same values. A nested
// patch is a nested record in the row, and the update merges it into the
// base's field from that record, as structMerge merges it from the literal.
func (bl *irScalarBuilder) recordedStructUpdate(t *ast.Call, k kind, base ir.Temp, lit *ast.StructLit) (ir.Temp, kind, bool, bool) {
	rec, rk, _, ok := bl.recordMake(lit)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	v, ok := bl.recordMerge(t, k, base, rec, rk, lit)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.qualRecordArgs = irQualArgs{temps: []ir.Temp{base, rec}, kinds: []kind{k, rk}, mobile: []bool{true, true}, ok: true}
	bl.qualRecordSeen = true
	return v, k, false, true
}

// recordMerge is structMerge over a patch already built as the record rec of
// kind rk: each field is read back from rec, and a nested patch merges the
// nested record into the base's field.
func (bl *irScalarBuilder) recordMerge(at ast.Node, k kind, base, rec ir.Temp, rk kind, lit *ast.StructLit) (ir.Temp, bool) {
	names := make([]string, len(lit.Fields))
	vals := make([]ir.Temp, len(lit.Fields))
	for i, f := range lit.Fields {
		fk, found := bl.g.updateField(k, f.Name)
		part, inRecord := anonFieldKind(rk, f.Name)
		if !found || !inRecord {
			return ir.NoTemp, false
		}
		p := ir.NewProjRecordField(bl.g.irNodePos(f.Value), bl.f.NewTemp(), rec, f.Name, irParamShape(part))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: part})
		if nested, isLit := f.Value.(*ast.StructLit); isLit && nested.TypeName == nil && nested.Spread == nil {
			if len(nested.Fields) == 0 || !irRetainedUpdateKind(fk) || part.tag != tagAnonStruct {
				return ir.NoTemp, false
			}
			sub, ok := bl.updateBaseField(f.Value, k, base, f.Name, fk)
			if !ok {
				return ir.NoTemp, false
			}
			v, ok := bl.recordMerge(f.Value, fk, sub, p.Dst(), part, nested)
			if !ok {
				return ir.NoTemp, false
			}
			names[i], vals[i] = f.Name, v
			continue
		}
		v, vk, ok := bl.coerceEmpty(f.Value, p.Dst(), part, fk)
		if !ok || vk != fk {
			return ir.NoTemp, false
		}
		names[i], vals[i] = f.Name, v
	}
	n := ir.NewMakeUpdate(bl.g.irNodePos(at), bl.f.NewTemp(), base, names, vals)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), true
}
