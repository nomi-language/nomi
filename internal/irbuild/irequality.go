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
//     derived struct's, enum's or distinct's), compared as shapeEquality
//     says;
//   - by an impl the machine computes natively (machineImpls: Int's,
//     Float's, String's, Bool's and Decimal's), one ir.Compare of the
//     scalar or the Decimal;
//   - by any other impl, a call of the one the record names: the
//     program's (equatableImplCall), or std's own (recordedCallPlan).
//
// An operator with no record the switch can read declines and is counted
// (noteUnreadOperator): one the builder writes with none carried
// onto it (carryOperatorUse), one nothing answers, and one over a type
// parameter its instance does not resolve. `!=` is `ir.Not` over `==`'s
// answer, or the comparison's own OpNe.
//
// Structural comparison is the VM's `ir.Compare` over the shape, which
// runs rt.Equal. A distinct is compared through its inner value and a
// tuple slot by slot, because `ir.Compare` admits neither shape and the
// VM's executor is not this package's to widen: structural equality on
// two distincts of one static type is its inner values' equality, and on
// two tuples it is every slot's. Each such part is compared as the
// checker's record at its type says (partEqualBody): a written impl by a
// call of it, any other by its shape.

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
	case machine == routeNative && (irScalarLeafKind(k) || isDecimalKind(k)):
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
		eq, ok := bl.structuralEqual(t, lhs, rhs, k, bl.g.operandCheckerType(t.Left))
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
// names over two values of kind k: a program's, from the gen that
// registered it (equatableImplCall), else std's own, planned
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
	eq, ok := bl.structuralEqual(t, lhs, rhs, k, bl.g.operandCheckerType(t.Left))
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

// structuralEqual is structural equality over two values of kind k and
// checker type st, built from the shapes `ir.Compare` admits: a distinct
// through its inner value and a tuple slot by slot, joined by `and`. Every
// operand is already a value, so the short circuit changes nothing
// observable. A part whose record names a written impl is compared by a
// call of it (partEqualBody); the kind chooses only the shape every other
// part is compared by.
func (bl *irScalarBuilder) structuralEqual(t *ast.Binary, lhs, rhs ir.Temp, k kind, st analysis.Type) (ir.Temp, bool) {
	if sym, written, ok := bl.partEqualBody(t, k, st); written {
		if !ok {
			return ir.NoTemp, false
		}
		call := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), sym, lhs, rhs)
		bl.b.Append(call)
		bl.side(call.Dst(), irScalarSide{k: kindBool})
		return call.Dst(), true
	}
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
		// A marker has one value, so a marker slot is equal.
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
		var inner analysis.Type
		if dt, ok := analysis.ResolveTypeVar(st).(*analysis.DistinctType); ok {
			inner = analysis.DistinctInner(dt)
		}
		return bl.structuralEqual(t, l, r, k.def.inner, inner)
	case irRetainedTupleKind(k):
		var acc ir.Temp
		for i, part := range k.comp.parts {
			l := bl.tupleProjection(t, lhs, i, part)
			r := bl.tupleProjection(t, rhs, i, part)
			eq, ok := bl.structuralEqual(t, l, r, part, partCheckerType(st, k, i))
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

// partEqualBody is the Equatable body a part of kind k and checker type
// st, compared inside the comparison t, is compared by: the program's
// impl the checker's record of `Equatable.equal` at st names
// (recordedIfaceFnOf, the resolution a comparator and a nested Debug body
// read), which the builder calls, as `==` on the part does. written is
// false where the record answers by shape or by dispatch, by an impl
// machineImpls computes (a derived impl among them: machineImpls is the
// one place that decides a derived Equatable is its type's shape), or by
// std's own impl, which rt reaches through the key table
// (irRecordStdKey): the part is then compared by its shape. ok is false
// for an impl the builder cannot call. A part with no checker type, or
// with a record its instance leaves unresolved, is compared by its shape
// and counted (noteElemImpl).
func (bl *irScalarBuilder) partEqualBody(t *ast.Binary, k kind, st analysis.Type) (sym *ir.Symbol, written, ok bool) {
	var c analysis.Callee
	if st != nil {
		c = bl.g.stdIfaceCallRecord(stdEquatable, "equal", 2, st).Callee
	}
	bl.noteElemImpl(t, "a part's equality", st, c.Form)
	if c.Form != analysis.OperatorByImpl || bl.g.machineRouteOf(operatorUseOfCall(stdEquatable, c, st)) != routeUnset {
		return nil, false, true
	}
	r := bl.recordedIfaceFnOf(c, stdEquatable, "equal", k, st, []kind{k, k}, kindBool)
	switch {
	case r.structural || r.std != nil || r.template != nil:
		return nil, false, true
	case r.local != nil:
		return bl.g.irImplSym(r.local), true, true
	case r.sym != nil:
		return r.sym, true, true
	}
	return nil, true, false
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

// bareVariantOwnKind is the kind of a payload-free prelude variant written
// with no expected type, `None == None` or `Maybe.equal(None, None)`: the
// type the checker records for it (valueKind), whose type arguments
// inference solved (`if c { None } else { Some(2.5) }` makes this None a
// Maybe<Float>) or closed (analysis's closed_vars.go). ok is false where n
// is no such variant; one whose record names no kind answers kindInvalid,
// which its caller declines.
func (bl *irScalarBuilder) bareVariantOwnKind(n ast.Node) (kind, bool) {
	name, line, col, ok := preludeValueName(n)
	if !ok {
		return kindInvalid, false
	}
	if _, _, vs, ok := bl.g.resolvedPreludeVariant(name, line, col); !ok || vs.carries() {
		return kindInvalid, false
	}
	k, _ := bl.valueKind("bare prelude variant", n)
	return k, true
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

// irScalarRangeKind reports a Range over an Int, a Float or a String, or a
// tuple or List of them: its fields are such values and a Maybe of one, so
// the VM compares and hashes it as a container by content
// (compareOperands), which is what std's Range, declaring no Equatable or
// Hashable impl, means by either.
func irScalarRangeKind(k kind) bool {
	elem, isRange := rangeElem(k)
	return isRange && irRetainedRangeKind(k) && irScalarRangeElem(elem)
}

// irScalarRangeElem is an Int, a Float or a String, or a tuple or non-empty
// List built of them.
func irScalarRangeElem(elem kind) bool {
	switch {
	case elem == kindInt || elem == kindFloat || elem == kindString:
		return true
	case (elem.tag == tagTuple || elem.tag == tagList) && elem.comp != nil && len(elem.comp.parts) > 0:
		for _, p := range elem.comp.parts {
			if !irScalarRangeElem(p) {
				return false
			}
		}
		return true
	}
	return false
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
	if use := g.carriedOperators[t]; use != nil {
		return use
	}
	return fa.Operators[t]
}

// carryOperatorUse makes c, the record of a call of iface's function for
// a receiver of type recv, the record of at, an operator the builder
// writes, until done is called (operatorUseOfCall). A record of another
// form carries nothing, and at then has none (noteUnreadOperator).
func (g *gen) carryOperatorUse(at ast.Node, iface analysis.StdType, c analysis.Callee, recv analysis.Type) (done func()) {
	return g.carryUse(at, operatorUseOfCall(iface, c, recv))
}

// carryUse makes use the record of at, an operator the builder writes,
// until done is called. A nil use carries nothing.
func (g *gen) carryUse(at ast.Node, use *analysis.OperatorUse) (done func()) {
	if use == nil {
		return func() {}
	}
	if g.carriedOperators == nil {
		g.carriedOperators = map[ast.Node]*analysis.OperatorUse{}
	}
	g.carriedOperators[at] = use
	return func() { delete(g.carriedOperators, at) }
}

// operatorUseOfCall is c, the record of a call of iface's function for a
// receiver of type recv, as the record of the operator the function is:
// by the impl c reaches, or by shape or by dispatch as c is. Nil for a
// record of another form.
func operatorUseOfCall(iface analysis.StdType, c analysis.Callee, recv analysis.Type) *analysis.OperatorUse {
	switch c.Form {
	case analysis.OperatorStructural, analysis.OperatorDispatched:
		return &analysis.OperatorUse{Form: c.Form}
	case analysis.OperatorByImpl:
		if m, ok := c.Decl.(ast.ImplMethodDecl); ok && !isNilNode(m) {
			return &analysis.OperatorUse{Form: c.Form, Impl: &analysis.OperatorImpl{
				Interface: iface.Name, Receiver: recv, Method: m}}
		}
	}
	return nil
}
