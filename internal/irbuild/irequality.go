package irbuild

// `==` and `!=` over nominal and structural values:
//
//   - a STRUCT runs its `impl Equatable`, derived or written, and compares
//     structurally when the program declares none;
//   - an ENUM or a DISTINCT runs a hand-written impl only; a derived impl or
//     none compares structurally;
//   - a record or a tuple compares structurally.
//
// Structural comparison is the VM's `ir.Compare` over the shape, which runs
// rt.Equal. A distinct is
// compared through its inner value and a tuple slot by slot, because
// `ir.Compare` admits neither shape and the VM's executor is not this
// package's to widen: structural equality on two distincts of one static type is its
// inner values' equality, and on two tuples it is every slot's.
//
// An impl this builder cannot call declines the comparison; a structural
// answer is only chosen once the impl's absence is established, because a
// missed lookup that became a structural comparison would be a wrong answer.
// `!=` is `ir.Not` over the same answer.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// enumEquality answers `==`/`!=` on an enum. owned is false for a kind that
// is not a retained enum.
func (bl *irScalarBuilder) enumEquality(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (v ir.Temp, owned, ok bool) {
	if k.tag != tagNamed || k.def == nil || !k.def.isEnum || !irRetainedEnumKind(k.def) || irParamShape(k) != ir.ValVariant {
		return ir.NoTemp, false, false
	}
	if og, d, hand, known := bl.g.handWrittenEquatableImpl(k); !known {
		return ir.NoTemp, true, false
	} else if hand {
		v, ok := bl.equatableImplCall(t, op, lhs, rhs, k, og, d)
		return v, true, ok
	}
	// An `embeds` variant is an enum record whose one field is the payload;
	// rt.Equal unwraps it, so the variant-shaped
	// comparison answers for it too.
	return bl.equalityCompare(t, op, ir.ValVariant, lhs, rhs), true, true
}

// valueEquality answers `==`/`!=` on a distinct, a struct, a record or a
// tuple. owned is false for any other kind.
func (bl *irScalarBuilder) valueEquality(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (v ir.Temp, owned, ok bool) {
	switch {
	case irRetainedRecordKind(k):
		return bl.equalityCompare(t, op, ir.ValStruct, lhs, rhs), true, true
	case irRetainedTupleKind(k):
		eq, ok := bl.structuralEqual(t, lhs, rhs, k)
		if !ok {
			return ir.NoTemp, true, false
		}
		return bl.equalityNegate(t, op, eq), true, true
	case k.tag != tagNamed || k.def == nil || isDecimalKind(k):
		return ir.NoTemp, false, false
	}
	d := k.def
	if d.isDistinct && !d.rtOpaque && irRetainedMarker(d) {
		// A zero-sized marker has one value, so two of them are equal,
		// which is rt.Equal's answer; a written Equatable impl still
		// decides for itself.
		og, impl, hand, known := bl.g.handWrittenEquatableImpl(k)
		switch {
		case !known:
			return ir.NoTemp, true, false
		case hand:
			v, ok := bl.equatableImplCall(t, op, lhs, rhs, k, og, impl)
			return v, true, ok
		}
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op != ir.OpNe)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool, deferrable: true})
		return c.Dst(), true, true
	}
	if d.isDistinct {
		if d.rtOpaque || !irDistinctOverValue(d) {
			return ir.NoTemp, false, false
		}
		og, impl, hand, known := bl.g.handWrittenEquatableImpl(k)
		switch {
		case !known:
			return ir.NoTemp, true, false
		case hand:
			v, ok := bl.equatableImplCall(t, op, lhs, rhs, k, og, impl)
			return v, true, ok
		}
		eq, ok := bl.structuralEqual(t, lhs, rhs, k)
		if !ok {
			return ir.NoTemp, true, false
		}
		return bl.equalityNegate(t, op, eq), true, true
	}
	if d.isEnum || !irRetainedStructKind(d) || irParamShape(k) != ir.ValStruct {
		// A std struct the VM holds as another shape, such as a Range, is
		// not compared here.
		return ir.NoTemp, false, false
	}
	if bl.g.noEquatableImpl(k) {
		return bl.equalityCompare(t, op, ir.ValStruct, lhs, rhs), true, true
	}
	og, impl := bl.g.equatableImplOf(k)
	if impl == nil {
		// A std impl, or one no table here holds: stdEquality's business,
		// or a decline.
		return ir.NoTemp, false, false
	}
	v, ok = bl.equatableImplCall(t, op, lhs, rhs, k, og, impl)
	return v, true, ok
}

// equalityCompare appends one structural comparison of the given shape.
func (bl *irScalarBuilder) equalityCompare(t *ast.Binary, op ir.CompareOp, shape ir.ValShape, lhs, rhs ir.Temp) ir.Temp {
	c := ir.NewCompare(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, shape, lhs, rhs)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst()
}

// equalityNegate turns an `==` answer into the operator's.
func (bl *irScalarBuilder) equalityNegate(t *ast.Binary, op ir.CompareOp, eq ir.Temp) ir.Temp {
	if op != ir.OpNe {
		return eq
	}
	n := ir.NewNot(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), eq)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: kindBool})
	return n.Dst()
}

// structuralEqual is structural equality over two values of kind k, built from the
// shapes `ir.Compare` admits: a distinct through its inner value and a tuple
// slot by slot, joined by `and`. Every operand is already a value, so the
// short circuit changes nothing observable.
func (bl *irScalarBuilder) structuralEqual(t *ast.Binary, lhs, rhs ir.Temp, k kind) (ir.Temp, bool) {
	switch {
	case k == kindInt || k == kindFloat || k == kindString || k == kindBool:
		return bl.equalityCompare(t, ir.OpEq, irParamShape(k), lhs, rhs), true
	case isDecimalKind(k):
		return bl.equalityCompare(t, ir.OpEq, ir.ValDecimal, lhs, rhs), true
	case irEqualityContainerKind(k):
		return bl.equalityCompare(t, ir.OpEq, ir.ValContainer, lhs, rhs), true
	case irRetainedRecordKind(k):
		return bl.equalityCompare(t, ir.OpEq, ir.ValStruct, lhs, rhs), true
	case k.tag == tagNamed && irRetainedStructKind(k.def) && irParamShape(k) == ir.ValStruct:
		return bl.equalityCompare(t, ir.OpEq, ir.ValStruct, lhs, rhs), true
	case k.tag == tagNamed && irRetainedEnumKind(k.def) && irParamShape(k) == ir.ValVariant:
		return bl.equalityCompare(t, ir.OpEq, ir.ValVariant, lhs, rhs), true
	case k.tag == tagNamed && irDistinctOverValue(k.def) && !k.def.rtOpaque:
		l := bl.distinctProjection(t, lhs, k, false, "irequality.go structuralEqual")
		r := bl.distinctProjection(t, rhs, k, false, "irequality.go structuralEqual")
		return bl.structuralEqual(t, l, r, k.def.inner)
	case irRetainedTupleKind(k):
		var acc ir.Temp
		for i, part := range k.comp.parts {
			l := bl.tupleProjection(t, lhs, i, part)
			r := bl.tupleProjection(t, rhs, i, part)
			eq, ok := bl.structuralEqual(t, l, r, part)
			if !ok {
				return ir.NoTemp, false
			}
			if i == 0 {
				acc = eq
				continue
			}
			acc = bl.equalityAnd(t, acc, eq)
		}
		return acc, true
	}
	return ir.NoTemp, false
}

// equalityAnd joins two computed Bool answers with the shared short circuit.
func (bl *irScalarBuilder) equalityAnd(t *ast.Binary, lhs, rhs ir.Temp) ir.Temp {
	pos := bl.g.irPos(t.Line, t.Col)
	dst := bl.f.NewTemp()
	sc := ir.BeginShortCircuit(bl.f.Region, bl.b, ir.LogicAnd, pos, pos, pos, dst, lhs)
	rightExit := sc.Rhs()
	bl.b = sc.Finish(rightExit, pos, rhs)
	bl.side(dst, irScalarSide{k: kindBool})
	return dst
}

// equatableImplCall calls the `equal?` of impl d, declared in og, over two
// values of kind k, and negates the answer for `!=`.
func (bl *irScalarBuilder) equatableImplCall(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, og *gen, d *implDef) (ir.Temp, bool) {
	if og == nil || d == nil || !d.lowerable {
		return ir.NoTemp, false
	}
	it := d.items["equal?"]
	if it == nil || !it.lowerable || len(it.params) != 2 || it.result != kindBool {
		return ir.NoTemp, false
	}
	for _, p := range it.params {
		if p.tag != tagNamed || p.def == nil || k.def == nil || p.def.decl != k.def.decl {
			return ir.NoTemp, false
		}
	}
	if u := irGenUnit(bl.g, og); og != bl.g && u < 0 {
		return ir.NoTemp, false
	}
	sym := og.irCalleeSym(it, d.recv.nomi()+"."+it.name)
	if bl.f.Sym() == sym {
		// `a == b` inside the impl it would call recurses without end.
		return ir.NoTemp, false
	}
	call := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), sym, lhs, rhs)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: kindBool})
	return bl.equalityNegate(t, op, call.Dst()), true
}

// irGenUnit is the file unit og builds, or -1.
func irGenUnit(g, og *gen) int {
	if g.reg == nil {
		return -1
	}
	for i, x := range g.reg.gens {
		if x == og {
			return i
		}
	}
	return -1
}

// equatableImplOf finds the `impl Equatable` for k's declaration in this gen
// or, for a type the program declares elsewhere, in the gen that holds it.
func (g *gen) equatableImplOf(k kind) (*gen, *implDef) {
	if d := g.implsByIface["Equatable"][k]; d != nil {
		return g, d
	}
	if g.reg == nil || k.def == nil || k.def.decl == nil {
		return nil, nil
	}
	for _, og := range g.reg.gens {
		if og == nil || og == g {
			continue
		}
		for _, d := range og.implOrder {
			if d.ifaceName == "Equatable" && d.recv.tag == tagNamed && d.recv.def != nil && d.recv.def.decl == k.def.decl {
				return og, d
			}
		}
	}
	return nil, nil
}

// handWrittenEquatableImpl reports the hand-written `impl Equatable` for an
// enum or distinct, program-wide. known is false when the absence of one
// cannot be established: a table still short of its declarations, or a std
// declaration whose impls this builder does not classify.
func (g *gen) handWrittenEquatableImpl(k kind) (og *gen, d *implDef, hand, known bool) {
	og, d = g.equatableImplOf(k)
	if d != nil {
		return og, d, !d.synth, true
	}
	if k.def.decl != nil && g.reg != nil && g.reg.byDecl[k.def.decl] != nil {
		// A user declaration: every unit's table is searched above, and
		// declaresEquatable answers "declared" while any is incomplete.
		return nil, nil, false, !g.reg.declaresEquatable(k.def.decl)
	}
	if k.def.foreign != "" {
		return nil, nil, false, false
	}
	if g.std != nil && len(g.std.byIface["Equatable.equal?"][k]) > 0 {
		// std declares no hand-written Equatable on an enum or a distinct
		// (see handWrittenNominalEquatable), so a std impl is derived.
		return nil, nil, false, true
	}
	return nil, nil, false, true
}

// bareVariantOwnKind types a payload-free prelude variant written with no
// expected type, `None == None` or `Maybe.equal?(None, None)`: the checker's
// solution for it when there is one, and otherwise the instance over Unit.
// A payload-free variant's value is the same whatever the type arguments are,
// so the choice decides the temporary's stated type and nothing the program
// can observe.
func (bl *irScalarBuilder) bareVariantOwnKind(n ast.Node) (kind, bool) {
	name, line, col, ok := preludeValueName(n)
	if !ok {
		return kindInvalid, false
	}
	a, sym, vs, ok := bl.g.resolvedPreludeVariant(name, line, col)
	if !ok || vs.carries() {
		return kindInvalid, false
	}
	args, ok := bl.g.checkedPreludeArgs(a, sym)
	if !ok {
		args = make([]kind, len(a.spec.params))
		for i := range args {
			args[i] = kindUnit
		}
	}
	k := bl.g.preludeInstance(a, args)
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return kindInvalid, false
	}
	return k, true
}

// equatableContainerOwner is the std container `Equatable.equal?(a, b)`
// compares, by the checker's instantiation of the call: "List", "Vector",
// "Map", "Maybe" or "Result", or "" for anything else.
func (bl *irScalarBuilder) equatableContainerOwner(t *ast.Call) string {
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != 2 {
		return ""
	}
	k := bl.g.project(ft.Params[0])
	if _, vector := vectorElem(k); vector {
		return "Vector"
	}
	switch k.tag {
	case tagList:
		return "List"
	case tagMap:
		return "Map"
	}
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil {
		return ""
	}
	switch k.def.preludeOf.spec {
	case preludeSpecFor("std/maybe", "Maybe"):
		return "Maybe"
	case preludeSpecFor("std/results", "Result"):
		return "Result"
	}
	return ""
}

// containerEqualCall lowers `List.equal?(a, b)`, `Vector.equal?`, `Map.equal?` and
// `Maybe.equal?(a, b)` to
// the comparison `a == b` is. The first is std's
// `impl Equatable for List<T>`, whose body is `a == b` over two lists and so
// structural; and the second is with Maybe's derived impl, which agrees with
// structural comparison wherever the payload has no hand-written impl — the
// set irPreludeEqualityKind admits.
//
// It owns the call once the owner and method match: the operands are lowered
// here, with a bare `None` typed by bareVariantOwnKind, and cannot be lowered
// a second time by a later arm.
func (bl *irScalarBuilder) containerEqualCall(t *ast.Call, owner string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// kindInvalid: sentinel — the operand at this position is not bare.
	bare := []kind{kindInvalid, kindInvalid}
	for i, arg := range t.Args {
		if !bl.bareEqualityOperand(arg, ir.OpEq) {
			continue
		}
		k, ok := bl.bareVariantOwnKind(arg)
		if !ok {
			return no()
		}
		bare[i] = k
	}
	saved := bl.qualBare
	bl.qualBare = bare
	args := bl.irQualLowerArgs(t)
	bl.qualBare = saved
	if !args.ok || len(args.kinds) != 2 {
		return no()
	}
	lk, rk := args.kinds[0], args.kinds[1]
	var shape ir.ValShape
	switch owner {
	case "List":
		ok := bl.untypedListPair(t, args)
		lk, rk = args.kinds[0], args.kinds[1]
		if !ok || lk != rk || !irEqualityListKind(lk) {
			return no()
		}
		shape = ir.ValContainer
	case "Vector", "Map":
		// std's `impl Equatable` for both is `a == b`, structural.
		if lk != rk || !irEqualityContainerKind(lk) {
			return no()
		}
		shape = ir.ValContainer
	case "Maybe", "Result":
		// Result's Equatable is derived, like Maybe's, so it agrees with
		// structural comparison over the payloads irPreludeEqualityKind admits.
		spec := preludeSpecFor("std/maybe", "Maybe")
		if owner == "Result" {
			spec = preludeSpecFor("std/results", "Result")
		}
		maybe := func(k kind) bool {
			return k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && k.def.preludeOf.spec == spec
		}
		if !maybe(lk) || !maybe(rk) {
			return no()
		}
		if rk != lk {
			// A bare `None` typed over Unit beside a solved operand: the
			// value is the same, so it is retyped to the other side's.
			at, want := 1, lk
			// kindInvalid: sentinel — the operand at this position is not bare.
			if bare[0] != kindInvalid {
				at, want = 0, rk
			}
			// kindInvalid: sentinel — neither operand is bare.
			if at == 1 && bare[1] == kindInvalid {
				return no()
			}
			v, k, _, ok := bl.preludeBareValue(t.Args[at], want)
			if !ok {
				return no()
			}
			args.temps[at], args.kinds[at] = v, k
			lk, rk = args.kinds[0], args.kinds[1]
		}
		if !bl.g.irPreludeEqualityKind(rk) || lk != rk {
			return no()
		}
		shape = ir.ValVariant
	default:
		return no()
	}
	c := ir.NewCompare(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), ir.OpEq, shape, args.temps[0], args.temps[1])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// irEqualityListKind is a list `==` compares with rt.Equal: equality never
// dispatches on a list, whatever its elements, so every list the builder
// represents compares structurally, with an element of a declared type
// compared through its own impl (vm/keys.go). That includes a list of a
// distinct over a tuple, which only irRetainedValueKind admits. A list of
// functions has no equality, so the checker rejects `==` before this.
func irEqualityListKind(k kind) bool {
	return k.tag == tagList && (irRetainedListKind(k) || irListTransportKind(k) || irRetainedValueKind(k))
}

// irEqualityContainerKind is a list, vector, map or set `==` compares with
// structural equality: equality dispatches on none of them (std's
// `impl Equatable` for Vector and Map is `a == b` itself), and the VM's
// `ir.Compare` over a container runs rt.Equal.
func irEqualityContainerKind(k kind) bool {
	if _, vector := vectorElem(k); vector && k != kindEmptyVector {
		// Any element: std's `impl Equatable for Vector<T>` is `a == b`,
		// and rt.Equal on a vector never dispatches.
		return true
	}
	return irEqualityListKind(k) || (k != kindEmptyVector && irRetainedVectorKind(k)) ||
		(k != kindEmptyMap && (irRetainedMapKind(k) || irMapTransportKind(k))) ||
		// std's Set Equatable is `a.items == b.items`, the membership maps'
		// structural equality, which rt.Equal over the Set record answers.
		(k != kindEmptySet && irRetainedSetKind(k))
}

// existentialDebugImpls names the Debug body of every type implementing k's
// interface in this unit, as nestedDebugImpls names a container's leaves.
// Any implementer whose Debug the renderer cannot name declines.
func (bl *irScalarBuilder) existentialDebugImpls(k kind) ([]ir.DebugImpl, bool) {
	var impls []ir.DebugImpl
	for _, d := range bl.g.implOrder {
		if d.iface != k.iface || d.recv.tag != tagNamed || d.recv.def == nil {
			continue
		}
		nested, ok := bl.nestedDebugImpls(named(d.recv.def))
		if !ok {
			return nil, false
		}
		impls = append(impls, nested...)
	}
	return impls, len(impls) != 0
}
