package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedTupleKind(k kind) bool {
	if k.tag != tagTuple || k.comp == nil || len(k.comp.parts) < 2 {
		return false
	}
	for _, part := range k.comp.parts {
		if !irCompositePartKind(part) {
			return false
		}
	}
	return true
}

// irCompositePartKind is a part a tuple or an anonymous record holds: a
// retained value, or a function value the callable domain holds. The VM
// carries a function in the composite like any other operand. `==` over a
// composite holding one never reaches the builder: the checker rejects it
// (functions have no equality), so structuralEqual never meets a function.
func irCompositePartKind(k kind) bool {
	return irRetainedValueKind(k) || (k.tag == tagFunc && irCallableValueKind(k))
}

func (bl *irScalarBuilder) tupleMake(t *ast.TupleLit) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no expected tuple type.
	return bl.tupleMakeWant(t, kindInvalid)
}

// tupleMakeWant is tupleMake with the position's expected tuple type, which
// types each component: `(0, [])` against `(Int, List<Int>)` builds its empty
// list at `List<Int>`.
func (bl *irScalarBuilder) tupleMakeWant(t *ast.TupleLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Items) < 2 {
		return no()
	}
	typed := want.tag == tagTuple && want.comp != nil && len(want.comp.parts) == len(t.Items)
	parts := make([]kind, len(t.Items))
	values := make([]ir.Temp, len(t.Items))
	for i, item := range t.Items {
		var v ir.Temp
		var k kind
		var ok bool
		if typed {
			v, k, _, ok = bl.lowerWant(item, want.comp.parts[i])
		} else {
			v, k, _, ok = bl.lower(item)
		}
		if !ok || !irCompositePartKind(k) {
			return no()
		}
		values[i], parts[i] = v, k
	}
	k := bl.g.tupleKind(parts)
	n := ir.NewMakeTuple(bl.g.irNodePos(t), bl.f.NewTemp(), values)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

func irTupleIndex(t *ast.FieldAccess) (int, bool) {
	if t.Field == nil {
		return 0, false
	}
	i, err := strconv.Atoi(t.Field.Name)
	return i, err == nil && i >= 0
}

func (bl *irScalarBuilder) tupleRead(t *ast.FieldAccess, index int) (ir.Temp, kind, bool, bool) {
	subj, k, mobile, ok := bl.lower(t.Object)
	if !ok || !irRetainedTupleKind(k) || index >= len(k.comp.parts) {
		return ir.NoTemp, kindInvalid, false, false
	}
	part := k.comp.parts[index]
	return bl.tupleProjection(t, subj, index, part), part, mobile, true
}

func (bl *irScalarBuilder) tupleProjection(at ast.Node, subj ir.Temp, index int, part kind) ir.Temp {
	n := ir.NewProjSlot(bl.g.irNodePos(at), bl.f.NewTemp(), subj, index, irParamShape(part))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: part})
	return n.Dst()
}

// destructureBind binds one name a destructuring statement introduces to the
// value read produces, under the plain binding's rebind rule: a fresh
// identity, declined where a callable is rebound.
// The subject is read before any component binds.
func (bl *irScalarBuilder) destructureBind(t ast.Node, name string, part kind, read func() ir.Temp) bool {
	sym := bl.sh.localSym(name)
	previous, shadows := bl.g.lookup(name)
	_, bound := bl.bound[name]
	if shadows || bound {
		oldKind := previous.k
		if bound {
			oldKind = bl.boundK[name]
		}
		// A VM-only test body may rebind a name it bound itself, under
		// the test-body binding's rule (testBinding).
		testRebind := bl.inTest && bound && !shadows
		if (bl.inTest && !testRebind) || oldKind.tag == tagFunc || part.tag == tagFunc {
			irDeclineNote("a rebind: " + name)
			return false
		}
		if bl.inTest {
			delete(bl.testStages, name)
			delete(bl.testDefs, name)
		}
		sym = ir.NewSymbol(name)
		bl.sh.syms[name] = sym
	}
	bind := ir.NewBind(bl.g.irNodePos(t), bl.f.NewTemp(), read(), sym)
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: part})
	bl.bound[name], bl.boundK[name] = bind.Dst(), part
	return true
}

// structBinding lowers `{x, y: b} = value` over an anonymous record or a
// retained struct: each named field is read off the held subject and bound.
// A field sub-pattern declines.
func (bl *irScalarBuilder) structBinding(t *ast.StructDestructure) bool {
	subj, k, _, ok := bl.lower(t.Value)
	if !ok {
		return false
	}
	record := irRetainedRecordKind(k)
	if !record && (k.tag != tagNamed || !irRetainedStructKind(k.def)) {
		irDeclineNote("a struct destructure outside retained records and structs")
		return false
	}
	type read struct {
		name, field string
		part        kind
		f           *fieldDef
	}
	reads := make([]read, 0, len(t.Fields))
	for _, fd := range t.Fields {
		if fd.Pattern != nil {
			irDeclineNote("a struct destructure with a field sub-pattern")
			return false
		}
		name := fd.Binding
		if name == "" {
			name = fd.Name
		}
		r := read{name: name, field: fd.Name}
		if record {
			part, found := anonFieldKind(k, fd.Name)
			if !found {
				return false
			}
			r.part = part
		} else {
			f := k.def.field(fd.Name)
			if f == nil {
				return false
			}
			r.part, r.f = f.k, f
		}
		reads = append(reads, r)
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		copy := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
		bl.b.Append(copy)
		bl.side(copy.Dst(), irScalarSide{k: k, copy: irCopyHold})
		subj = copy.Dst()
	}
	for _, r := range reads {
		if ast.IsDiscardName(r.name) {
			continue
		}
		r := r
		project := func() ir.Temp {
			if record {
				n := ir.NewProjRecordField(bl.g.irNodePos(t), bl.f.NewTemp(), subj, r.field, irParamShape(r.part))
				bl.b.Append(n)
				bl.side(n.Dst(), irScalarSide{k: r.part})
				return n.Dst()
			}
			p := ir.NewProjField(bl.g.irNodePos(t), bl.f.NewTemp(), subj,
				bl.g.irTypes().Symbol(r.f, r.field), r.field, irParamShape(r.part))
			bl.b.Append(p)
			bl.side(p.Dst(), irScalarSide{k: r.part})
			return p.Dst()
		}
		if !bl.destructureBind(t, r.name, r.part, project) {
			return false
		}
	}
	bl.irDiscardStmtUnit(t)
	return true
}

func (bl *irScalarBuilder) tupleBinding(t *ast.TupleDestructure) bool {
	subj, k, _, ok := bl.lower(t.Value)
	if !ok || !irRetainedTupleKind(k) || len(k.comp.parts) != len(t.Bindings) {
		if ok {
			irDeclineNote("a tuple destructure outside the retained tuple domain")
		}
		return false
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		copy := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
		bl.b.Append(copy)
		bl.side(copy.Dst(), irScalarSide{k: k, copy: irCopyHold})
		subj = copy.Dst()
	}
	for i, id := range t.Bindings {
		if id == nil || ast.IsDiscardName(id.Name) {
			continue
		}
		part := k.comp.parts[i]
		if !bl.destructureBind(t, id.Name, part, func() ir.Temp { return bl.tupleProjection(t, subj, i, part) }) {
			return false
		}
	}
	bl.irDiscardStmtUnit(t)
	return true
}
