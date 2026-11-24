package irbuild

// A bare `..` (*ast.CurrentValue) in an update literal: the value already at
// the field or key the literal is written at.
//
//	.{..user, address: .{.., city: "NYC"}}
//	{..team, "address" => {.., "city" => "Atlanta"}}
//	.{..account, transactions: [t, ..]}
//
// The update that owns the position reads the old value there and records it
// for the `..` (bl.current) before it lowers the literal, which then lowers as
// the update it is: a struct spread (structSpread), a map spread (mapUpdate),
// a set spread (setUpdate) or a list spread (listMakeOf), each with the old
// value as its base. There is no second lowering of any of them. A `..`
// operand in the value (`visits: .. + 1`, `street: rename(..)`) reads the
// same record, so the base is evaluated once however many `..`s there are;
// each `..` belongs to the innermost field or key whose value holds it
// (ast.CurrentValuesOf), whose update records it later and so wins. A `..`
// in a lambda written in the value is captured from the builder that holds
// it, as a captured name is (captureCurrent).
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
	"strconv"

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

// setCurrent records what each bare `..` that belongs to the field or key
// whose value is value stands for (ast.CurrentValuesOf): the spread of an
// update literal there, and every `..` operand in it.
func (bl *irScalarBuilder) setCurrent(value ast.Node, c irCurrent) {
	for _, cv := range ast.CurrentValuesOf(value) {
		if bl.current == nil {
			bl.current = map[*ast.CurrentValue]irCurrent{}
		}
		bl.current[cv] = c
	}
}

// readsCurrent reports whether value, written at a field or key of an
// update, holds a bare `..` of its own, so the update reads the old value
// there before lowering it.
func readsCurrent(value ast.Node) bool {
	return len(ast.CurrentValuesOf(value)) > 0
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
	if !ok && bl.parent != nil {
		return bl.captureCurrent(t)
	}
	if !ok {
		bl.g.dc.note("a bare `..` with no current value recorded")
		return ir.NoTemp, kindInvalid, false, false
	}
	if !c.missing {
		return c.v, c.k, false, true
	}
	return bl.emptyOf(t, c.k)
}

// captureCurrent lowers a `..` written in a lambda inside a field's value
// (`street: Option.map(m, fn s -> s + ..)`): the value the enclosing builder
// holds for it, made a parameter of this closure as a captured name is
// (capture), once per `..`.
func (bl *irScalarBuilder) captureCurrent(t *ast.CurrentValue) (ir.Temp, kind, bool, bool) {
	if held, ok := bl.capturedCurrent[t]; ok {
		return held.temp, held.k, true, true
	}
	src, k, _, ok := bl.parent.currentValue(t)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if k == kindUnit {
		u := ir.NewUnit(bl.g.irNodePos(t), bl.f.NewTemp())
		bl.b.Append(u)
		return u.Dst(), kindUnit, true, true
	}
	if !irCallableValueKind(k) {
		// The enclosing builder's `..` is a field's current value, of a
		// field kind a lowerable struct holds, Unit handled above.
		panic("captureCurrent: a current value of kind " + k.nomi() + ", outside the callable domain")
	}
	dst := bl.g.irAddParam(bl.f, ir.NewSymbol(".."+strconv.Itoa(t.Line)+":"+strconv.Itoa(t.Col)), k)
	bl.captures = append(bl.captures, src)
	if bl.capturedCurrent == nil {
		bl.capturedCurrent = map[*ast.CurrentValue]irLambdaCapture{}
	}
	bl.capturedCurrent[t] = irLambdaCapture{temp: dst, k: k, src: src}
	return dst, k, true, true
}

// emptyOf builds the empty collection of kind k: a list, set, map or
// vector. It is the one constructor of an empty collection: a literal (`[]`,
// `Set[]`, `{}`) is built at the kind the checker gave it (emptyLiteral), and
// a value the builder makes for a position (a bare `..` at a missing key, a
// std field's `[]` default) at the position's kind. The value has its kind
// from the start; nothing retypes it later. An empty Vector, Set or Map is
// made where it is written, as a literal with elements is, so it is a
// fresh value the function may update in place (ir package doc, "Updates
// written in place"); an empty List is a constant, since nothing writes a
// List in place.
func (bl *irScalarBuilder) emptyOf(at ast.Node, k kind) (ir.Temp, kind, bool, bool) {
	pos := bl.g.irNodePos(at)
	var dst ir.Temp
	pure := false
	switch {
	case k.tag == tagList && irRetainedValueKind(k):
		c := ir.NewEmptyList(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		dst = c.Dst()
	case k.tag == tagMap && irRetainedMapKind(k):
		c := ir.NewMakeMap(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		dst, pure = c.Dst(), true
	case irRetainedVectorKind(k) || bl.g.irVectorValueKind(k):
		c := ir.NewMakeVector(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		dst, pure = c.Dst(), true
	case irRetainedSetKind(k):
		c := ir.NewMakeSet(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		dst, pure = c.Dst(), true
	default:
		bl.g.dc.note("an empty collection of kind " + k.nomi())
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.side(dst, irScalarSide{k: k, pureMake: pure})
	return dst, k, true, true
}

// updateBuild lowers the struct update `{.., f: v}` at a position that holds
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
	if bk.tag != tagMap || bk.comp == nil || len(bk.comp.parts) != 2 {
		bl.g.dc.note("a bare `..` at a key of a map whose kind is open")
		return ir.NoTemp, kindInvalid, false
	}
	vk := bk.comp.parts[1]
	mk, ok := bl.g.optionOf(vk, value)
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
		// lowerWant answers vk when ok.
		v, _, _, ok := bl.lowerWant(value, vk)
		if !ok {
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
		if readsCurrent(f.Value) {
			return true
		}
	}
	return false
}

// setUpdate builds `[..base, x]`, a set literal with one spread written
// first: the base, then the elements as a set literal, then
// `Set.union(base, literal)`, so the base's elements keep their places and
// new ones follow, as Map.merge orders a map's keys.
func (bl *irScalarBuilder) setUpdate(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spread := t.Spreads[0].Head
	base, bk, _, ok := bl.lower(spread)
	if !ok {
		return no()
	}
	// A base that is another Iter (`[..option, x]`) is made a Set.
	p, ok := bl.collectSpread(t, spreadPart{at: spread, v: base, k: bk}, setSpreadJoin)
	if !ok {
		return no()
	}
	base, bk = p.v, p.k
	if len(t.Items) == 0 {
		// `[..option]`: the Iter made a Set is the literal.
		return base, bk, false, true
	}
	// The elements, a copy of the literal without its spread, are a set of
	// the base's kind, the literal's (collectSpread).
	items := *t
	items.Spreads = nil
	lit, lk, _, ok := bl.setMakeOf(&items, bk)
	if !ok {
		return no()
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
// field in place (`{address: {.., city: "NYC"}}`) when the patch is evaluated
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
			if readsCurrent(f.Value) {
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
// `[..v, x]` as `Vector.concat(v, [x])` and `[x, ..v]` as
// `Vector.concat([x], v)`, where v is the spread, or the vector a bare `..`
// stands for. The spread is lowered before the elements, as a list spread's
// tail is. An empty spread (`[..[], x]`) takes the elements' kind.
func (bl *irScalarBuilder) vectorUpdate(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spread := t.Spreads[0].Head
	spreadLast := t.Spreads[0].At > 0
	base, bk, _, ok := bl.lower(spread)
	if !ok {
		return no()
	}
	// A spread that is another Iter (`[x, ..option]`) is made a Vector.
	p, ok := bl.collectSpread(t, spreadPart{at: spread, v: base, k: bk}, vectorSpreadJoin)
	if !ok {
		return no()
	}
	base, bk = p.v, p.k
	if len(t.Items) == 0 {
		// `[..option]`: the Iter made a Vector is the literal.
		return base, bk, false, true
	}
	if _, isVector := vectorElem(bk); !isVector {
		return no()
	}
	// The elements, a copy of the literal without its spread, are a vector
	// of the spread's kind, the literal's (collectSpread).
	items := *t
	items.Spreads = nil
	lit, lk, _, ok := bl.vectorMakeOf(&items, bk)
	if !ok {
		return no()
	}
	if lit, lk, ok = bl.convertTo(t, lit, lk, bk); !ok {
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
