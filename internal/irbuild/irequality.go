package irbuild

// `==` and `!=` are lowered by the checker's record of the operator
// (analysis.FileAnalysis.Operators, at an instance's types: operatorUse),
// one arm for each form it answers (equality):
//
//   - by dispatch: two interface values compare by their run-time types,
//     through the impl of the value each holds (ir.ValIface);
//   - by shape: a type with no block of Equatable, a tuple, a record, a
//     declared type with no impl (spec §5 *Equality bounds*), and an impl
//     the machine computes by shape (machineImpls: List's, Unit's, a
//     derived enum's or distinct's), compared as shapeEquality says;
//   - by an impl the machine computes natively (machineImpls: Int's,
//     Float's, String's and Bool's), one ir.Compare of the scalar;
//   - by any other impl, a call of the one the record names: the
//     program's, derived or written (equatableImplCall), or std's own
//     (recordedCallPlan).
//
// An operator with no record the switch can read declines and is counted
// (noteUnreadOperator): one the builder writes with none carried
// onto it (carryOperator), one nothing answers, and one over a type
// parameter its instance does not resolve. `!=` is `ir.Not` over `==`'s
// answer, or the comparison's own OpNe.
//
// Structural comparison is the VM's `ir.Compare` over the shape, which
// runs rt.Equal. A distinct is compared through its inner value and a
// tuple slot by slot, because `ir.Compare` admits neither shape and the
// VM's executor is not this package's to widen: structural equality on
// two distincts of one static type is its inner values' equality, and on
// two tuples it is every slot's.

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// equality lowers `==` or `!=` over two lowered operands of kind k by the
// form the checker recorded for the operator t, a by-impl record split by
// the route machineImpls gives its impl. An arm that cannot lower its
// operands, and an operator with no record to read, decline here.
func (bl *irScalarBuilder) equality(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, bool) {
	use := bl.g.operatorUse(t)
	var form analysis.OperatorForm
	if use != nil {
		form = use.Form
	}
	machine := bl.g.machineRouteOf(use)
	switch {
	case form == analysis.OperatorDispatched && irHeldKind(k):
		return bl.equalityCompare(t, op, ir.ValIface, lhs, rhs), true
	case machine == routeNative && irScalarLeafKind(k):
		return bl.equalityCompare(t, op, irParamShape(k), lhs, rhs), true
	case form == analysis.OperatorStructural || machine != routeUnset:
		// By shape, and an impl the machine computes natively over a value
		// it holds as no scalar: std/bool's True or False bound by
		// `Bool.False(x)`, a marker of one value.
		if v, ok := bl.shapeEquality(t, op, lhs, rhs, k); ok {
			return v, true
		}
	case form == analysis.OperatorByImpl && machine == routeUnset && use.Impl != nil:
		if v, ok := bl.implEquality(t, op, use, lhs, rhs, k, rmobile); ok {
			return v, true
		}
	}
	bl.g.noteUnreadOperator(t, familyEquality)
	return ir.NoTemp, false
}

// shapeEquality compares two values of kind k by their shape: Unit and a
// marker, which have one value, by a constant; an enum by its variant; a
// record, a struct and a container (a List, a Map, a Set, a Vector, a
// Byte or Bytes, a Regex's handle, a Dynamic, a `Type<T>` witness, a
// Range over scalars) as one comparison rt.Equal answers; a tuple and a
// distinct through their parts (structuralEqual). Any other kind
// declines.
func (bl *irScalarBuilder) shapeEquality(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (ir.Temp, bool) {
	d := k.def
	named := k.tag == tagNamed && d != nil
	switch {
	case k == kindUnit:
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op == ir.OpEq)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), true
	case named && d.isEnum && irRetainedEnumKind(d) && irParamShape(k) == ir.ValVariant:
		// An `embeds` variant is an enum record whose one field is the
		// payload; rt.Equal unwraps it, so the variant-shaped comparison
		// answers for it too.
		return bl.equalityCompare(t, op, ir.ValVariant, lhs, rhs), true
	case irRetainedRecordKind(k) || irRetainedTupleKind(k):
		return bl.tupleRecordEquality(t, op, lhs, rhs, k)
	case named && !isDecimalKind(k) && d.isDistinct && !d.rtOpaque && irRetainedMarker(d):
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op != ir.OpNe)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool, deferrable: true})
		return c.Dst(), true
	case named && !isDecimalKind(k) && d.isDistinct && !d.rtOpaque && irDistinctOverValue(d):
		eq, ok := bl.structuralEqual(t, lhs, rhs, k)
		if !ok {
			return ir.NoTemp, false
		}
		return bl.equalityNegate(t, op, eq), true
	case named && !isDecimalKind(k) && !d.isEnum && !d.isDistinct && irRetainedStructKind(d) && irParamShape(k) == ir.ValStruct:
		return bl.equalityCompare(t, op, ir.ValStruct, lhs, rhs), true
	case irEqualityContainerKind(k) || irByteValueKind(k) || irStdHandleKind(k) || irDynamicKind(k) ||
		irTypeWitnessKind(k) || irScalarRangeKind(k):
		// std's Byte and Bytes are rt leaves rt.Equal compares by
		// content, a Regex by its pattern, which rt.Equal reads from the
		// handle (rt.HostKeyed), a Dynamic by its contents
		// (rt.DynamicEqual), a `Type<T>` witness by the type it names
		// (rt.TypeWitnessEqual), and a Range over scalars by its fields,
		// as Range's Equatable does; the VM holds each as a container.
		return bl.equalityCompare(t, op, ir.ValContainer, lhs, rhs), true
	}
	return ir.NoTemp, false
}

// implEquality calls the impl of Equatable the operator t's record, use,
// names over two values of kind k: a program's, derived or written, from
// the gen that registered it (equatableImplCall), else std's own, planned
// as a call of it (operatorCall, recordedCallPlan): its `equal` body or
// host.
func (bl *irScalarBuilder) implEquality(t *ast.Binary, op ir.CompareOp, use *analysis.OperatorUse, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, bool) {
	if og, it := bl.g.recordedImplItem(use.Impl.Method, nil, k); it != nil {
		return bl.equatableImplCall(t, op, lhs, rhs, k, og, it.impl)
	}
	call, done := bl.operatorCall(t)
	defer done()
	args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
	if p := bl.recordedCallPlan(call, args, "Equatable.equal"); p != nil && p.result == kindBool {
		if v, _, _, ok := bl.qualEmit(call, args, p); ok {
			return bl.equalityNegate(t, op, v), true
		}
	}
	return ir.NoTemp, false
}

// tupleRecordEquality answers `==`/`!=` on a retained record or tuple, k:
// a record is one structural comparison, and a tuple compares part by part.
func (bl *irScalarBuilder) tupleRecordEquality(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (ir.Temp, bool) {
	if irRetainedRecordKind(k) {
		return bl.equalityCompare(t, op, ir.ValStruct, lhs, rhs), true
	}
	eq, ok := bl.structuralEqual(t, lhs, rhs, k)
	if !ok {
		return ir.NoTemp, false
	}
	return bl.equalityNegate(t, op, eq), true
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
	case k == kindUnit:
		// Unit has one value, so a Unit slot is always equal.
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), true)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), true
	case isDecimalKind(k):
		return bl.equalityCompare(t, ir.OpEq, ir.ValDecimal, lhs, rhs), true
	case irHeldKind(k):
		return bl.equalityCompare(t, ir.OpEq, ir.ValIface, lhs, rhs), true
	case irByteValueKind(k):
		// rt.Equal compares a Byte or Bytes by content, which is the body
		// of std's own `impl Equatable` for each; binary's bytes route.
		return bl.equalityCompare(t, ir.OpEq, ir.ValContainer, lhs, rhs), true
	case irEqualityContainerKind(k) || irScalarRangeKind(k) || irStdHandleKind(k) || irDynamicKind(k) || irTypeWitnessKind(k):
		// A Range over a scalar is a container rt.Equal compares, a Regex
		// a handle it compares by pattern, a Dynamic a tree it compares
		// by contents, and a witness the type it names, as compareOperands'
		// routes say.
		return bl.equalityCompare(t, ir.OpEq, ir.ValContainer, lhs, rhs), true
	case k.tag == tagNamed && irRetainedMarker(k.def):
		// A marker has one value, so a marker slot is equal unless a
		// written Equatable impl says otherwise. A slot has no record of
		// its own, so the impl is found by the slot's kind
		// (handWrittenEquatableImpl). An impl not yet known (one another
		// unit declares) has no block here, which equatableImplCall
		// declines.
		if og, impl, hand, known := bl.g.handWrittenEquatableImpl(k); hand || !known {
			return bl.equatableImplCall(t, ir.OpEq, lhs, rhs, k, og, impl)
		}
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), true)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), true
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

// equatableImplCall calls the `equal` of impl d, declared in og, over two
// values of kind k, and negates the answer for `!=`.
func (bl *irScalarBuilder) equatableImplCall(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, og *gen, d *implDef) (ir.Temp, bool) {
	if og == nil || d == nil || !d.lowerable {
		return ir.NoTemp, false
	}
	it := d.items["equal"]
	if it == nil || !it.lowerable || len(it.params) != 2 || it.result != kindBool {
		return ir.NoTemp, false
	}
	for _, p := range it.params {
		if p.tag != tagNamed || p.def == nil || k.def == nil || p.def.decl != k.def.decl {
			return ir.NoTemp, false
		}
	}
	if u := irGenUnit(bl.g, og); og != bl.g && u < 0 && !irStdProgramGen(bl.g, og) {
		return ir.NoTemp, false
	}
	// Inside the impl's own `equal` this is a recursive call: on fields
	// of the type (a tree's children) it ends with the data, and the
	// checker rejects comparing the impl's own arguments
	// (internal/analysis/self_recursion.go, protocolErrors).
	sym := og.irImplSym(it)
	call := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), sym, lhs, rhs)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: kindBool})
	return bl.equalityNegate(t, op, call.Dst()), true
}

// irStdProgramGen reports whether og is one of the program's gens a std
// instance g is lowered for: its symbols link by name, as a std instance's
// calls to a program's Debug impls do.
func irStdProgramGen(g, og *gen) bool {
	if g.stdModule == "" || g.stdInsts == nil {
		return false
	}
	for _, p := range g.stdInsts.program {
		if p == og {
			return true
		}
	}
	return false
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
	if d := g.implsByIface[stdIfaceImplKey(stdEquatable)][k]; d != nil {
		return g, d
	}
	if k.def != nil && k.def.decl != nil && g.stdModule != "" && g.stdInsts != nil {
		// A std instance at a program's type (`Iter.contains<Point>`): the
		// impl is the program's, in one of its gens.
		for _, og := range g.stdInsts.program {
			if og == nil {
				continue
			}
			if d := og.implsByIface[stdIfaceImplKey(stdEquatable)][k]; d != nil {
				return og, d
			}
			for _, d := range og.implOrder {
				if d.ifaceKey == stdIfaceImplKey(stdEquatable) && d.recv.tag == tagNamed && d.recv.def != nil && d.recv.def.decl == k.def.decl {
					return og, d
				}
			}
		}
	}
	if g.reg == nil || k.def == nil || k.def.decl == nil {
		return nil, nil
	}
	if k.def.instOrigin != nil {
		// A mirror of another file's generic instance: its impls are
		// registered against the owner's instance, never in implOrder.
		return g.instanceImpl(k.def, stdIfaceImplKey(stdEquatable), "equal")
	}
	for _, og := range g.reg.gens {
		if og == nil || og == g {
			continue
		}
		for _, d := range og.implOrder {
			if d.ifaceKey == stdIfaceImplKey(stdEquatable) && d.recv.tag == tagNamed && d.recv.def != nil && d.recv.def.decl == k.def.decl {
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
	if g.std != nil && len(g.std.byIface["Equatable.equal"][k]) > 0 {
		// std declares no hand-written Equatable on an enum or a distinct
		// (see handWrittenNominalEquatable), so a std impl is derived.
		return nil, nil, false, true
	}
	return nil, nil, false, true
}

// bareVariantOwnKind types a payload-free prelude variant written with no
// expected type, `None == None` or `Maybe.equal(None, None)`: the checker's
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
		// The type the checker gave this value, once inference solved it
		// (`if c { None } else { Some(2.5) }` makes this None a
		// Maybe<Float>).
		if k, solved := bl.g.recordedPreludeKind(n, a); solved {
			return k, true
		}
		args = make([]kind, len(a.spec.params))
		for i := range args {
			args[i] = kindUnit
		}
	}
	k := bl.g.preludeInstance(a, args)
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		// args are checkedPreludeArgs', each a retained prelude payload
		// (irRetainedPreludePayload), or Unit, and a prelude instance over
		// such payloads is retained, as Task.outcome's is (taskCall).
		panic("bareVariantOwnKind: " + name + " at " + k.nomi() + ", outside the retained domain")
	}
	return k, true
}

// equatableContainerOwner is the std container `Equatable.equal(a, b)`
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

// containerEqualCall lowers `List.equal(a, b)`, `Vector.equal`, `Map.equal` and
// `Maybe.equal(a, b)` to
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
	if (owner == "Maybe" || owner == "Result") && bl.recording == 0 && !bl.preludeEqualStructural(t) {
		// `Maybe.equal(a, b)` over payloads whose own impls decide (a
		// struct with a derived Equatable) or of two instances an embedded
		// value tells apart (`Some(circle)`, `Some(shape)`): the prelude
		// wrapper's Equatable is derived, so the call is `a == b`, lowered
		// as the operator is, by the call's record (carryOperator).
		at := &ast.Binary{Op: "==", Left: t.Args[0], Right: t.Args[1], Line: t.Line, Col: t.Col}
		defer bl.g.carryOperator(at, t)()
		return bl.compare(at, ir.OpEq)
	}
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
		if lk != rk || !irEqualityListKind(lk) {
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
				panic("irbuild: containerEqualCall: the loop above sets at to the operand that is bare, so at == 1 implies bare[1] is set")
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
		panic("irbuild: containerEqualCall: reached for " + owner + ", which equatableContainerOwner does not name")
	}
	c := ir.NewCompare(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), ir.OpEq, shape, args.temps[0], args.temps[1])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// preludeEqualStructural reports whether the checker typed both operands of
// a `Maybe.equal` or `Result.equal` call as one instance whose payloads
// compare structurally (irPreludeEqualityKind), which containerEqualCall
// compares directly.
func (bl *irScalarBuilder) preludeEqualStructural(t *ast.Call) bool {
	if len(t.Args) != 2 {
		return true
	}
	tl, tr := bl.g.checkedExprType(t.Args[0]), bl.g.checkedExprType(t.Args[1])
	if tl == nil || tr == nil {
		return true
	}
	l, r := bl.g.project(tl), bl.g.project(tr)
	return l == r && bl.g.irPreludeEqualityKind(l)
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
	if _, vector := vectorElem(k); vector {
		// Any element: std's `impl Equatable for Vector<T>` is `a == b`,
		// and rt.Equal on a vector never dispatches.
		return true
	}
	return irEqualityListKind(k) || irRetainedVectorKind(k) ||
		irRetainedMapKind(k) || irMapTransportKind(k) ||
		// std's Set Equatable is `a.items == b.items`, the membership maps'
		// structural equality, which rt.Equal over the Set record answers.
		irRetainedSetKind(k)
}

// existentialDebugImpls names the Debug body of every type implementing k's
// interface in this unit (in every unit of the program, for a std instance
// body), as nestedDebugImpls names a container's leaves.
// Any implementer whose Debug the renderer cannot name declines, and so
// does an interface nothing implements or boxes into, except std's
// Display: a scalar or a std type enters it unboxed and unrecorded
// (irErases), and renders as itself. A box whose value holds k itself (a
// `List<Display>` boxed into Display) names k's implementers once: the
// walk that reaches k again inside them adds nothing.
func (bl *irScalarBuilder) existentialDebugImpls(k kind) ([]ir.DebugImpl, bool) {
	if irConjKind(k) {
		// A conjunction holds a value of a type implementing every member,
		// so the implementers of one name its Debug.
		return bl.existentialDebugImpls(k.comp.parts[0])
	}
	bl.g.noteDebugRendered(k)
	if bl.g.debugImplsVisiting[k] {
		return nil, true
	}
	if bl.g.debugImplsVisiting == nil {
		bl.g.debugImplsVisiting = map[kind]bool{}
	}
	bl.g.debugImplsVisiting[k] = true
	defer delete(bl.g.debugImplsVisiting, k)
	if s := bl.g.stdInsts; bl.g.stdModule != "" && s != nil && len(s.program) > 0 && k.iface != nil {
		// A std instance's body at a program's interface or at std's
		// Display or Debug (`List.inspect<Show>`, `List.inspect<Debug>`):
		// the implementers and boxes are the program's, in every unit.
		var impls []ir.DebugImpl
		seen := map[ir.DebugImpl]bool{}
		held := false
		for _, og := range s.program {
			if og == nil {
				continue
			}
			got, found, ok := (&irScalarBuilder{g: og}).unitExistentialDebugImpls(k, false)
			if !ok {
				return nil, false
			}
			held = held || found
			for _, im := range got {
				if !seen[im] {
					seen[im] = true
					impls = append(impls, im)
				}
			}
		}
		return impls, held || irStdDisplayExistential(k)
	}
	impls, found, ok := bl.unitExistentialDebugImpls(k, true)
	if !ok {
		return nil, false
	}
	if reg := bl.g.reg; reg != nil && k.iface != nil {
		// The program's other files implement and box into the interface
		// too: `struct Pool { conn: Conn }` in the file that declares
		// Conn, and `impl Conn for Memory` in the file that builds one.
		seen := make(map[ir.DebugImpl]bool, len(impls))
		for _, im := range impls {
			seen[im] = true
		}
		for _, og := range reg.gens {
			if og == nil || og == bl.g {
				continue
			}
			got, held, ok := (&irScalarBuilder{g: og}).unitExistentialDebugImpls(k, false)
			if !ok {
				return nil, false
			}
			found = found || held
			for _, im := range got {
				if !seen[im] {
					seen[im] = true
					impls = append(impls, im)
				}
			}
		}
	}
	return impls, found || irStdDisplayExistential(k)
}

// unitExistentialDebugImpls is existentialDebugImpls over this unit's impls
// and boxes. same says k's interface is this unit's own def; otherwise an
// impl of the interface is one of its declaration at k's arguments. found
// says the unit implements or boxes into the interface; ok is false when
// some implementer's Debug cannot be named.
func (bl *irScalarBuilder) unitExistentialDebugImpls(k kind, same bool) (impls []ir.DebugImpl, found, ok bool) {
	of := func(d *ifaceDef) bool {
		if same {
			return d == k.iface
		}
		return d != nil && sameIfaceInstance(d, k.iface)
	}
	// The impl blocks this unit declares, and those it registered at each
	// instance of a generic type it declares (`impl Labeled for Bx<T>` at
	// Bx<Int>, registerInstanceImpls), whose template block is no
	// implementer of its own.
	implementers := slices.Clone(bl.g.implOrder)
	for _, inst := range bl.g.genericImplQueue {
		implementers = append(implementers, inst.impl)
	}
	for _, d := range implementers {
		if !of(implIface(d)) || d.recv.tag != tagNamed || d.recv.def == nil {
			continue
		}
		nested, ok := bl.nestedDebugImpls(nil, named(d.recv.def), d.recvType)
		if !ok {
			// An `embeds` enum enters the interface boxed under its name.
			nested, ok = bl.boxedEnumDebugImpls(named(d.recv.def))
		}
		if !ok && !irDebugValueKind(d.recv) {
			// A std container instance with no declared leaf (an impl for
			// `Maybe<Int>` or `Vector<Int>`) renders structurally.
			return nil, false, false
		}
		impls = append(impls, nested...)
	}
	// A container or `embeds` enum this unit boxes into the interface
	// (irboxed.go): the box renders as its value, whose nested declared
	// types are named as a container's leaves are.
	boxed := false
	for _, key := range bl.g.boxes.order {
		if p := bl.g.boxes.plans[key]; p == nil || !of(key.iface) {
			continue
		}
		nested, ok := bl.nestedDebugImpls(nil, key.have, nil)
		if !ok {
			nested, ok = bl.boxedEnumDebugImpls(key.have)
		}
		if !ok && !irDebugValueKind(key.have) {
			return nil, false, false
		}
		impls = append(impls, nested...)
		boxed = true
	}
	for d, kinds := range bl.g.stdProgHeld {
		// A std type held unboxed as a std interface (stdprogiface.go)
		// renders as itself.
		if of(d) && len(kinds) > 0 {
			boxed = true
		}
	}
	// Every value the checker typed entering the interface in this unit's
	// source, whether or not the body that boxes it is lowered yet: a body
	// lowered before the one that boxes a List into it (a derived Debug, a
	// function declared first) names the same implementers.
	for _, e := range bl.g.checkedErasures() {
		if !of(e.iface) {
			continue
		}
		for _, have := range e.kinds {
			nested, ok := bl.nestedDebugImpls(nil, have, nil)
			if !ok {
				nested, ok = bl.boxedEnumDebugImpls(have)
			}
			if !ok && !irDebugValueKind(have) {
				return nil, false, false
			}
			impls = append(impls, nested...)
			boxed = true
		}
	}
	return impls, len(impls) != 0 || boxed, true
}

// recordedPreludeKind is the instance of a's prelude enum the checker
// recorded as n's type, or false.
func (g *gen) recordedPreludeKind(n ast.Node, a *preludeAnchor) (kind, bool) {
	if g.fa == nil {
		return kindInvalid, false
	}
	et, ok := resolvedEnumType(g.srcFA().ExprTypes[n])
	if !ok || len(et.TypeArgs) != len(a.spec.params) {
		return kindInvalid, false
	}
	args := make([]kind, len(et.TypeArgs))
	for i, ty := range et.TypeArgs {
		args[i] = g.project(ty)
		if !irRetainedPreludePayload(args[i]) {
			return kindInvalid, false
		}
	}
	k := g.preludeInstance(a, args)
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || k.def.preludeOf.spec != a.spec || !irRetainedEnumKind(k.def) {
		return kindInvalid, false
	}
	return k, true
}

// resolvedEnumType is ty, through solved inference variables, as an enum.
func resolvedEnumType(ty analysis.Type) (*analysis.EnumType, bool) {
	for {
		tv, isVar := ty.(*analysis.TypeVar)
		if !isVar || tv.Resolved == nil {
			break
		}
		ty = tv.Resolved
	}
	et, ok := ty.(*analysis.EnumType)
	return et, ok && et != nil
}

// operatorUse is the checker's record of what the operator t means, nil
// for one the builder wrote with no record carried onto it and one
// nothing answers:
// inside an instance, the record at the instance's types where the
// checker resolves it there (operatorUseAt), so an operator over a type
// parameter reaches the impl a written one at that type does.
func (g *gen) operatorUse(t ast.Node) *analysis.OperatorUse {
	use, _ := g.operatorUseAt(t)
	return use
}

// operatorRecord is the record the checker made for the operator t
// (analysis.FileAnalysis.Operators), as the body was checked, or for an
// operator the builder wrote, the record it carries (carryOperatorUse).
func (g *gen) operatorRecord(t ast.Node) *analysis.OperatorUse {
	fa := g.srcFA()
	if fa == nil || t == nil {
		return nil
	}
	if b, ok := t.(*ast.Binary); ok {
		if use := g.carriedOperators[b]; use != nil {
			return use
		}
	}
	return fa.Operators[t]
}

// carryOperator makes the record of the call t, of `Equatable.equal` or
// a std type's `equal` (`Maybe.equal(a, b)`), at this instance (calleeAt)
// the record of at, the `a == b` the builder writes for it, until done is
// called (carryOperatorUse), for t's first operand's type.
func (g *gen) carryOperator(at *ast.Binary, t *ast.Call) (done func()) {
	return g.carryOperatorUse(at, stdEquatable, g.calleeAt(t), g.firstOperandType(t))
}

// carryOperatorUse makes c, the record of a call of iface's function for
// a receiver of type recv, the record of at, an operator the builder
// writes, until done is called: by the impl c reaches, or by shape or by
// dispatch as c is. A record of another form carries nothing, and at
// then has none (noteUnreadOperator).
func (g *gen) carryOperatorUse(at *ast.Binary, iface analysis.StdType, c analysis.Callee, recv analysis.Type) (done func()) {
	var use *analysis.OperatorUse
	switch c.Form {
	case analysis.OperatorStructural, analysis.OperatorDispatched:
		use = &analysis.OperatorUse{Form: c.Form}
	case analysis.OperatorByImpl:
		if m, ok := c.Decl.(ast.ImplMethodDecl); ok && !isNilNode(m) {
			use = &analysis.OperatorUse{Form: c.Form, Impl: &analysis.OperatorImpl{
				Interface: iface.Name, Receiver: recv, Method: m}}
		}
	}
	if use == nil {
		return func() {}
	}
	if g.carriedOperators == nil {
		g.carriedOperators = map[*ast.Binary]*analysis.OperatorUse{}
	}
	g.carriedOperators[at] = use
	return func() { delete(g.carriedOperators, at) }
}
