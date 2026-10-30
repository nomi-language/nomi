package irbuild

// A bare `..` (*ast.CurrentValue) in an update literal: the value already at
// the field or key the literal is written at.
//
//	#{..user, address: #{.., city: "NYC"}}
//	%{..team, "address" => %{.., "city" => "Atlanta"}}
//	#{..account, transactions: [t, ..]}
//
// The update that owns the position reads the old value there and records it
// for the `..` (bl.current) before it lowers the literal, which then lowers as
// the update it is: a struct spread (structSpread), a map spread (mapUpdate),
// a set spread (setUpdate) or a list spread (listMake), each with the old
// value as its base. There is no second lowering of any of them.
//
// A struct field always has an old value, projected from the base. A map key
// may not: the old value is `Map.get(base, key)`, and the literal is lowered
// on both edges of a test of it. Where the key is present the `..` is the
// value found; where it is missing the `..` stands for nothing, which the
// literal builds from: an empty collection, or, for a struct, the fields it
// writes and the struct's field defaults (updateBuild). The checker refused a
// struct with an unwritten field that has no default, so a missing key never
// traps.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irCurrent is what one bare `..` stands for while its literal is lowered.
type irCurrent struct {
	v ir.Temp
	k kind
	// missing: the position holds no value (a map key the base lacks, or a
	// position under one), so the literal builds the value from nothing.
	missing bool
}

// setCurrent records what the bare `..` of value stands for, when value is
// an update literal (ast.UpdateHole).
func (bl *irScalarBuilder) setCurrent(value ast.Node, c irCurrent) {
	hole := ast.UpdateHole(value)
	if hole == nil {
		return
	}
	if bl.current == nil {
		bl.current = map[*ast.CurrentValue]irCurrent{}
	}
	bl.current[hole] = c
}

// currentMissing reports whether lit's bare `..` stands for a position that
// holds no value.
func (bl *irScalarBuilder) currentMissing(spread ast.Node) (irCurrent, bool) {
	cv, ok := spread.(*ast.CurrentValue)
	if !ok {
		return irCurrent{}, false
	}
	c, ok := bl.current[cv]
	return c, ok && c.missing
}

// currentValue lowers a bare `..`: the old value at its position, or, where
// the position holds none, the empty collection of its kind.
func (bl *irScalarBuilder) currentValue(t *ast.CurrentValue) (ir.Temp, kind, bool, bool) {
	c, ok := bl.current[t]
	if !ok {
		irDeclineNote("a bare `..` with no current value recorded")
		return ir.NoTemp, kindInvalid, false, false
	}
	if !c.missing {
		return c.v, c.k, false, true
	}
	return bl.emptyOf(t, c.k)
}

// emptyOf builds the empty collection of kind k: a list, set, map or vector.
func (bl *irScalarBuilder) emptyOf(at ast.Node, k kind) (ir.Temp, kind, bool, bool) {
	pos := bl.g.irNodePos(at)
	var v ir.Temp
	var have kind
	switch {
	case k.tag == tagList:
		c := ir.NewEmptyList(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindEmptyList})
		v, have = c.Dst(), kindEmptyList
	case k.tag == tagMap:
		c := ir.NewMakeMap(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindEmptyMap, pureMake: true})
		v, have = c.Dst(), kindEmptyMap
	default:
		if _, isVector := vectorElem(k); isVector {
			c := ir.NewEmptyVector(pos, bl.f.NewTemp(), nil)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: kindEmptyVector})
			v, have = c.Dst(), kindEmptyVector
			break
		}
		if _, isSet := setElem(k); isSet {
			c := ir.NewEmptySet(pos, bl.f.NewTemp(), nil)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: kindEmptySet})
			v, have = c.Dst(), kindEmptySet
			break
		}
		irDeclineNote("a bare `..` at a missing key of kind " + k.nomi())
		return ir.NoTemp, kindInvalid, false, false
	}
	v, got, ok := bl.coerceEmpty(at, v, have, k)
	if !ok || got != k {
		return ir.NoTemp, kindInvalid, false, false
	}
	return v, k, false, true
}

// updateBuild lowers the struct update `#{.., f: v}` at a position that holds
// no value: the struct of kind k built from the fields written and, for a
// declared struct, the defaults of the others. A field written as an update
// literal holds no value either, and is built the same way.
func (bl *irScalarBuilder) updateBuild(t *ast.StructLit, k kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	for _, f := range t.Fields {
		fk, found := bl.g.updateField(k, f.Name)
		if !found {
			return no()
		}
		bl.setCurrent(f.Value, irCurrent{k: fk, missing: true})
	}
	switch {
	case k.tag == tagNamed && k.def != nil:
		return bl.structMakeOf(t, k.def)
	case irRetainedRecordKind(k):
		whole := *t
		whole.Spread = nil
		return bl.recordMakeOf(&whole, k)
	}
	return no()
}

// holdTemp forces v into a temporary of its own when it is not one already,
// so a value read twice (a base, a key) is evaluated once.
func (bl *irScalarBuilder) holdTemp(at ast.Node, v ir.Temp, k kind) ir.Temp {
	if irHeldValue(bl.f, v, bl.sides) {
		return v
	}
	c := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), v)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyHold})
	return c.Dst()
}

// updateEntry lowers value, an update literal at key of the map update whose
// base is base (of map kind bk): `Map.get(base, key)` is tested, and value is
// lowered where the key is present with the value found as its `..`, and
// where it is missing with nothing as its `..`. Both edges write the result
// slot, which is the entry's value.
func (bl *irScalarBuilder) updateEntry(value ast.Node, key, base ir.Temp, bk kind) (ir.Temp, kind, bool) {
	if bk.tag != tagMap || bk == kindEmptyMap || bk.comp == nil || len(bk.comp.parts) != 2 {
		irDeclineNote("a bare `..` at a key of a map whose kind is open")
		return ir.NoTemp, kindInvalid, false
	}
	vk := bk.comp.parts[1]
	mk, ok := bl.g.maybeOf(vk, value)
	if !ok || mk.def == nil {
		return ir.NoTemp, kindInvalid, false
	}
	some := mk.def.variant("Some")
	ty := bl.g.irTypeOf(vk)
	if some == nil || len(some.payloads) != 1 || ty == nil {
		return ir.NoTemp, kindInvalid, false
	}
	pos := bl.g.irNodePos(value)
	get := ir.NewHostCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym("Map.get", "Map.get"), base, key)
	bl.b.Append(get)
	bl.side(get.Dst(), irScalarSide{k: mk})
	found := bl.holdTemp(value, get.Dst(), mk)
	match := bl.variantTest(value, found, mk.def, some)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	slot := bl.f.NewTemp()
	head := bl.b
	head.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: vk})
	present := bl.f.NewBlock(pos, "update key present")
	missing := bl.f.NewBlock(pos, "update key missing")
	join := bl.f.NewBlock(pos, "update join")
	head.SetTerm(ir.NewBranch(pos, match.Dst(), present.ID(), missing.ID()))
	edge := func(b *ir.Block, c irCurrent) bool {
		bl.b = b
		if !c.missing {
			c.v = bl.variantPayload(value, found, mk.def, some)
		}
		bl.setCurrent(value, c)
		v, k, _, ok := bl.lowerWant(value, vk)
		if !ok {
			return false
		}
		if v, k, ok = bl.coerceEmpty(value, v, k, vk); !ok || k != vk {
			return false
		}
		bl.b.Append(ir.NewCopy(pos, slot, v))
		bl.b.SetTerm(ir.NewJump(pos, join.ID()))
		return true
	}
	if !edge(present, irCurrent{k: vk}) || !edge(missing, irCurrent{k: vk, missing: true}) {
		return ir.NoTemp, kindInvalid, false
	}
	bl.b = join
	return slot, vk, true
}

// patchHasHole reports whether a field of the patch lit is an update literal.
func patchHasHole(lit *ast.StructLit) bool {
	for _, f := range lit.Fields {
		if ast.UpdateHole(f.Value) != nil {
			return true
		}
	}
	return false
}

// setUpdate builds `%[..base, x]`, a set literal with one spread written
// first: the base, then the elements as a set literal, then
// `Set.union(base, literal)`, so the base's elements keep their places and
// new ones follow, as Map.merge orders a map's keys.
func (bl *irScalarBuilder) setUpdate(t *ast.SetLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spread := t.Spreads[0].Head
	base, bk, _, ok := bl.lower(spread)
	if !ok {
		return no()
	}
	items := *t
	items.Spreads = nil
	// kindInvalid: sentinel — an open base gives the elements no kind.
	elem := kindInvalid
	if e, isSet := setElem(bk); isSet && bk != kindEmptySet {
		elem = e
	}
	lit, lk, _, ok := bl.setMakeOf(&items, elem)
	if !ok {
		return no()
	}
	if bk == kindEmptySet {
		if base, bk, ok = bl.coerceEmpty(spread, base, bk, lk); !ok {
			return no()
		}
	}
	if bk != lk || !irRetainedSetKind(lk) {
		return no()
	}
	c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym("Set.union", "Set.union"), base, lit)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: lk, deferrable: true})
	return c.Dst(), lk, false, true
}

// structUpdateHoles lowers `Struct.update(base, patch)` whose patch updates a
// field in place (`#{address: #{.., city: "NYC"}}`) when the patch is evaluated
// before the base (it is named first) or recorded for an assertion's
// `values:` row. An update literal reads its field from the base, so it is
// lowered after the base; every other field keeps its place in the order the
// arguments are written. The recorded patch is the record of the values the
// fields took.
func (bl *irScalarBuilder) structUpdateHoles(t *ast.Call, ops structUpdateArgs, lit *ast.StructLit, recorded bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	pre := map[int]irLowered{}
	if ops.patchFirst {
		for i, f := range lit.Fields {
			if ast.UpdateHole(f.Value) != nil {
				continue
			}
			v, k, mobile, ok := bl.lower(f.Value)
			if !ok {
				return no()
			}
			if !mobile {
				cp := ir.NewCopy(bl.g.irNodePos(f.Value), bl.f.NewTemp(), v)
				bl.b.Append(cp)
				bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
				v = cp.Dst()
			}
			pre[i] = irLowered{v: v, k: k}
		}
	}
	base, k, _, ok := bl.lower(argExpr(t.Args[ops.base]))
	if !ok || !irRetainedUpdateKind(k) {
		return no()
	}
	names, vals, ok := bl.structMergeVals(k, base, lit, pre)
	if !ok {
		return no()
	}
	n := ir.NewMakeUpdate(bl.g.irNodePos(t), bl.f.NewTemp(), base, names, vals)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	if recorded {
		parts := make([]kind, len(names))
		byName := make(map[string]ir.Temp, len(names))
		for i, name := range names {
			fk, _ := bl.g.updateField(k, name)
			parts[i], byName[name] = fk, vals[i]
		}
		rk := bl.g.anonStructKind(names, parts)
		if !irRetainedRecordKind(rk) {
			return no()
		}
		ops2 := make([]ir.Temp, len(rk.comp.names))
		for i, name := range rk.comp.names {
			ops2[i] = byName[name]
		}
		rec := ir.NewMakeRecord(bl.g.irNodePos(lit), bl.f.NewTemp(), rk.comp.names, ops2)
		bl.b.Append(rec)
		bl.side(rec.Dst(), irScalarSide{k: rk})
		temps, kinds := make([]ir.Temp, 2), make([]kind, 2)
		temps[ops.base], kinds[ops.base] = base, k
		temps[ops.patch], kinds[ops.patch] = rec.Dst(), rk
		bl.qualRecordArgs = irQualArgs{temps: temps, kinds: kinds, mobile: []bool{true, true}, ok: true}
		bl.qualRecordSeen = true
	}
	return n.Dst(), k, false, true
}

// vectorUpdate builds a vector literal with one spread at either end:
// `#[..v, x]` as `Vector.concat(v, #[x])` and `#[x, ..v]` as
// `Vector.concat(#[x], v)`, where v is the spread, or the vector a bare `..`
// stands for. The spread is lowered before the elements, as a list spread's
// tail is. An empty spread (`#[..#[], x]`) takes the elements' kind.
func (bl *irScalarBuilder) vectorUpdate(t *ast.VectorLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spread := t.Spreads[0].Head
	spreadLast := t.Spreads[0].At > 0
	base, bk, _, ok := bl.lower(spread)
	if !ok {
		return no()
	}
	// kindInvalid: sentinel — an open base gives the elements no kind.
	elem := kindInvalid
	if e, isVector := vectorElem(bk); isVector && bk != kindEmptyVector {
		elem = e
	} else if bk != kindEmptyVector {
		return no()
	}
	items := *t
	items.Spreads = nil
	lit, lk, _, ok := bl.vectorMakeOf(&items, elem)
	if !ok {
		return no()
	}
	if bk == kindEmptyVector {
		if base, bk, ok = bl.coerceEmpty(spread, base, bk, lk); !ok {
			return no()
		}
	}
	if lit, lk, ok = bl.coerceEmpty(t, lit, lk, bk); !ok || lk != bk {
		return no()
	}
	first, second := base, lit
	if spreadLast {
		first, second = lit, base
	}
	c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym("Vector.concat", "Vector.concat"), first, second)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: bk, deferrable: true})
	return c.Dst(), bk, false, true
}
