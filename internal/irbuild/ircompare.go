package irbuild

// Comparisons and short-circuits in retained functions and test bodies.
//
// # The comparison
//
// `ir.Compare` is the node and internal/ir/compare.go argues its existence.
// The producer's job is to establish what that node's constructor cannot: that
// both operands are the SAME shape and that the shape is in the retained
// scalar, typed primitive-leaf list or anchored prelude enum domain. Mixed kinds and dispatched
// equality remain outside this producer. Prelude enum equality selects the existing structural
// comparator, with scalar payloads and no handwritten Equatable impl. Local struct
// equality selects it only when no Equatable impl exists for the struct.
//
// # The short-circuit, and the asymmetric row that is the reason it is here
//
// The SHAPE is `ir.BeginShortCircuit`'s, shared, and this producer must not
// rebuild it: `irlogic.go` records that putting the right-operand block on the
// wrong arm "evaluates something Nomi says must not be evaluated".
//
// What this file adds is the recording rule, which is not symmetric:
//
//	or    shows its left operand ALWAYS
//	and   shows its left operand ONLY WHEN THE LEFT DECIDED
//
// because a true left explains nothing about why a conjunction failed.
// `testdata/tests_short_circuit.nomi` is the fixture — `assert 1 < 2 and 3 < 2` prints one row and
// `assert 1 > 2 or 3 < 2` prints two. THE RULE IS OBSERVABLE ONLY ON A FAILING
// PATH, so nothing but a deliberately-red fixture can see it, which is what
// makes this the population worth reaching.
//
// `and`'s CONDITIONAL ROW IS A DIAMOND AHEAD OF THE SHORT-CIRCUIT: one branch
// records the row when the left decided, and the short-circuit's own branch
// follows over the same condition. The recording is pure, so a diamond around
// it changes what is EVALUATED not at all.
//
// # The right operand's row comes before the Copy
//
// This appends the right operand's row and then calls `Finish`, which appends
// the Copy. The reason is mechanical: `Block.Append` panics after a terminator
// and `Finish` sets one. The row reads the right operand's temporary and the
// Copy writes the destination, so no value either instruction reads is written
// by the other, and the report's row ORDER — which is what a transcript
// compares — is the order the rows are appended in.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irCompareOps is the source spelling of each comparison this producer builds.
//
// A TABLE RATHER THAN A SWITCH, exactly as `irArithOps` is, so that
// `binary`'s dispatch is a lookup and the admitted set is readable as data.
var irCompareOps = map[string]ir.CompareOp{
	"==": ir.OpEq,
	"!=": ir.OpNe,
	"<":  ir.OpLt,
	"<=": ir.OpLe,
	">":  ir.OpGt,
	">=": ir.OpGe,
}

// compare lowers `a <op> b` over two operands of one retained comparison shape.
func (bl *irScalarBuilder) compare(t *ast.Binary, op ir.CompareOp) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bare, ok := bl.bareEqualityOperands(t, op); ok {
		return bare()
	}
	if bl.bareEqualityOperand(t.Left, op) && bl.bareEqualityOperand(t.Right, op) {
		// `None == None`: neither side has a type, so the checker's (or
		// Unit's) instance types both. Two constants; no effect moves.
		k, ok := bl.bareVariantOwnKind(t.Left)
		if !ok {
			return no()
		}
		lhs, lk, _, ok := bl.preludeBareValue(t.Left, k)
		if !ok {
			return no()
		}
		rhs, rk, rmobile, ok := bl.preludeBareValue(t.Right, k)
		if !ok {
			return no()
		}
		return bl.compareOperands(t, op, lhs, lk, rhs, rk, rmobile)
	}
	lhs, lk, mobile, ok := bl.lower(t.Left)
	if !ok {
		return no()
	}
	if !mobile {
		// An impure left operand is forced into a temporary so a later
		// operand's statements cannot be hoisted ahead of it. `bl.binary`'s
		// own arithmetic path does the same, with the same node and the same
		// position.
		c := ir.NewCopy(bl.g.irNodePos(t.Left), bl.f.NewTemp(), lhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: lk, copy: irCopyForce})
		lhs = c.Dst()
	}
	var rhs ir.Temp
	var rk kind
	var rmobile bool
	if bl.bareEqualityOperand(t.Right, op) {
		// `x == None`: the bare variant takes the other operand's type, as an
		// argument or a return takes its parameter's.
		rhs, rk, rmobile, ok = bl.preludeBareValue(t.Right, lk)
	} else if c, isCall := t.Right.(*ast.Call); isCall && !op.Ordering() && lk.tag == tagNamed && lk.def != nil && lk.def.preludeOf != nil {
		// `x == Maybe.Some(v)`: the checker solved both operands as one
		// type, so the constructor takes the other operand's instance where
		// the call site recorded none (a stdlib prompt's).
		at, n := bl.b, len(bl.b.Instrs())
		if rhs, rk, rmobile, ok = bl.preludeCallWant(c, lk); !ok {
			if bl.b != at || len(bl.b.Instrs()) != n {
				// The attempt lowered the payload; lowering it again
				// would run its effects twice.
				return no()
			}
			rhs, rk, rmobile, ok = bl.lower(t.Right)
		}
	} else {
		rhs, rk, rmobile, ok = bl.lower(t.Right)
	}
	if !ok {
		return no()
	}
	return bl.compareOperands(t, op, lhs, lk, rhs, rk, rmobile)
}

// bareEqualityOperand reports an equality operand that is a bare payload-free
// prelude variant (`None`), which has no type of its own. Only a test body
// built for the VM takes it: the test-body reader does not spell it, and a
// named function's comparison does not admit it.
func (bl *irScalarBuilder) bareEqualityOperand(n ast.Node, op ir.CompareOp) bool {
	if op.Ordering() {
		return false
	}
	name, line, col, ok := preludeValueName(n)
	if !ok {
		return false
	}
	// `Direction.North` has the same spelling as `Maybe.None`; only a
	// payload-free PRELUDE variant lacks a type of its own.
	_, _, vs, prelude := bl.g.resolvedPreludeVariant(name, line, col)
	return prelude && !vs.carries()
}

// bareEqualityOperands lowers `None == x`: the right operand first, since the
// left has no type until the right supplies one. A bare variant is a constant,
// so no effect moves; the rows are recorded in source order after both.
func (bl *irScalarBuilder) bareEqualityOperands(t *ast.Binary, op ir.CompareOp) (func() (ir.Temp, kind, bool, bool), bool) {
	if !bl.bareEqualityOperand(t.Left, op) || bl.bareEqualityOperand(t.Right, op) {
		return nil, false
	}
	return func() (ir.Temp, kind, bool, bool) {
		rhs, rk, rmobile, ok := bl.lower(t.Right)
		if !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
		if !rmobile && bl.recording > 0 {
			c := ir.NewCopy(bl.g.irNodePos(t.Right), bl.f.NewTemp(), rhs)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: rk, copy: irCopyForce})
			rhs, rmobile = c.Dst(), true
		}
		lhs, lk, _, ok := bl.preludeBareValue(t.Left, rk)
		if !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
		return bl.compareOperands(t, op, lhs, lk, rhs, rk, rmobile)
	}, true
}

// irEmptyCollectionKind is an empty Map, Set or Vector literal's kind, which
// has no element types until a consuming position supplies them.
func irEmptyCollectionKind(k kind) bool {
	return k == kindEmptyMap || k == kindEmptySet || k == kindEmptyVector
}

// compareOperands is `compare` once both operands are lowered.
func (bl *irScalarBuilder) compareOperands(t *ast.Binary, op ir.CompareOp, lhs ir.Temp, lk kind,
	rhs ir.Temp, rk kind, rmobile bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	ok := true
	if !op.Ordering() {
		if lk.tag == tagEmptyList && rk.tag == tagEmptyList {
			// `[] == []`: no element is read, so the element type is
			// unobservable; see untypedListPair.
			want := listKindIn(bl.g, kindInt)
			if lhs, lk, ok = bl.coerceEmpty(t.Left, lhs, lk, want); ok {
				rhs, rk, ok = bl.coerceEmpty(t.Right, rhs, rk, want)
			}
		} else if lk.tag == tagList && rk.tag == tagEmptyList {
			rhs, rk, ok = bl.coerceEmpty(t.Right, rhs, rk, lk)
		} else if rk.tag == tagList && lk.tag == tagEmptyList {
			lhs, lk, ok = bl.coerceEmpty(t.Left, lhs, lk, rk)
		} else if irEmptyCollectionKind(rk) && !irEmptyCollectionKind(lk) {
			// `v == #[]`: the empty Vector, Set or Map takes the other
			// operand's element types.
			rhs, rk, ok = bl.coerceEmpty(t.Right, rhs, rk, lk)
		} else if irEmptyCollectionKind(lk) && !irEmptyCollectionKind(rk) {
			lhs, lk, ok = bl.coerceEmpty(t.Left, lhs, lk, rk)
		}
	}
	if !ok || lk != rk {
		return no()
	}
	if bl.recording > 0 && !rmobile {
		// Inside a recording subject the right operand is named twice, once
		// in the record and once in the comparison, so an impure one is
		// forced into a temporary first. Without it the reader would splice
		// the expression into both places, and `assert 2 == f(1)` would call
		// `f` twice and print a row computed by the first call. Outside a
		// recording subject `bl.record` is a no-op, so no hoist is needed.
		c := ir.NewCopy(bl.g.irNodePos(t.Right), bl.f.NewTemp(), rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: rk, copy: irCopyForce})
		rhs, rmobile = c.Dst(), true
	}
	// The rows, in source order: both operands of `== != < > <= >=` are
	// worth showing, and the redundancy rule that drops `1 = 1` is rt's to
	// apply. Recorded before any route is chosen, so a comparison answered
	// by an impl call shows its operands too.
	bl.record(t.Left, lhs, lk, true)
	bl.record(t.Right, rhs, rk, true)
	if op.Ordering() && lk.tag == tagNamed && !isDecimalKind(lk) {
		if bl.g.implsByIface["Comparable"][lk] == nil && bl.g.stdCompareAt(lk, stdEnumKind(stdEnumOrdering)) != nil {
			// A std type's own `impl Comparable` (Date, Time, a Vector
			// is below): its compare answers an Ordering.
			if _, isVector := vectorElem(lk); !isVector {
				return bl.stdRankedCompare(t, op, lhs, rhs, lk, rmobile)
			}
		}
		if _, isVector := vectorElem(lk); !isVector || bl.g.implsByIface["Comparable"][lk] != nil {
			return bl.rankedCompare(t, op, lhs, rhs, lk)
		}
	}
	if lk == kindUnit && !op.Ordering() {
		// Unit has one value, so `==` is True and `!=` False once both
		// operands have run, as rt.Equal answers.
		c := ir.NewBool(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op == ir.OpEq)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), kindBool, false, true
	}
	list := irEqualityContainerKind(lk)
	prelude := bl.g.irPreludeEqualityKind(lk)
	decimal := isDecimalKind(lk)
	if !op.Ordering() {
		// See irequality.go: an enum first, since std's derived enum impls
		// compare structurally, then std's own impls, then the
		// remaining nominal and structural kinds.
		if v, owned, ok := bl.enumEquality(t, op, lhs, rhs, lk); owned {
			if !ok {
				return no()
			}
			return v, kindBool, false, true
		}
		if v, ok := bl.stdEquality(t, lhs, rhs, lk, rmobile); ok {
			return bl.equalityNegate(t, op, v), kindBool, false, true
		}
		if v, owned, ok := bl.valueEquality(t, op, lhs, rhs, lk); owned {
			if !ok {
				return no()
			}
			return v, kindBool, false, true
		}
	}
	if op.Ordering() && decimal {
		return bl.stdRankedCompare(t, op, lhs, rhs, lk, rmobile)
	}
	if op.Ordering() && (lk == kindString || lk.tag == tagList) {
		return bl.comparableRankedCompare(t, op, lhs, rhs, lk, rmobile)
	}
	if _, isVector := vectorElem(lk); op.Ordering() && isVector {
		return bl.comparableRankedCompare(t, op, lhs, rhs, lk, rmobile)
	}
	structural := bl.g.irStructEqualityKind(lk)
	// std's Byte and Bytes are rt leaves rt.Equal compares by content,
	// which is also the body of std's own `impl Equatable` for each
	// (`a == b`); the VM compares them as it does a container.
	bytes := !op.Ordering() && irByteValueKind(lk)
	list = list || bytes
	if elem, isRange := rangeElem(lk); isRange && !op.Ordering() && irRetainedRangeKind(lk) &&
		(elem == kindInt || elem == kindFloat || elem == kindString) {
		// A Range over a scalar: its fields are scalars and a Maybe of one,
		// so Range's Equatable and rt.Equal answer alike; the VM holds
		// it as a container rt.Equal compares.
		list = true
	}
	if (!irScalarLeafKind(lk) || lk.def != nil) && !prelude && !decimal && !list && !structural {
		// Other named types need program-scoped method selection. A missed
		// impl lookup must never silently become structural comparison.
		return no()
	}
	shape := irParamShape(lk)
	if list && !op.Ordering() {
		// A Vector is a generic host type, whose parameter shape is unknown;
		// as an equality operand it is a container like a List or a Map.
		shape = ir.ValContainer
	}
	if shape == ir.ValUnknown || (op.Ordering() && shape != ir.ValInt && shape != ir.ValFloat && !(shape == ir.ValBool && lk == kindBool)) {
		// Ordering outside Int and Float declines. Asked here as well as in
		// `ir.NewCompare` because the constructor panics, as a producer-bug
		// fence, and a Nomi program that writes `"a" < "b"` must decline
		// rather than crash.
		return no()
	}
	c := ir.NewCompare(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, shape, lhs, rhs)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	// IMPURE, for `bl.binary`'s reason about every arith shape: the answer is
	// computed by an instruction rather than named by a local, so a
	// comparison in a left-operand position hoists.
	return c.Dst(), kindBool, false, true
}

// Only anchored prelude enums with scalar or Decimal payloads use structural comparison
// here. User nominal types take the program-scoped Equatable selection.
func (g *gen) irPreludeEqualityKind(k kind) bool {
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) || k.def.preludeOf == nil || g.handWrittenNominalEquatable(k) {
		return false
	}
	spec := k.def.preludeOf.spec
	outcome := spec == preludeSpecFor("std/tasks", "Outcome")
	if !outcome && spec != preludeSpecFor("std/maybe", "Maybe") && spec != preludeSpecFor("std/results", "Result") && spec != preludeSpecFor("std/literals", "Fragment") {
		return false
	}
	for _, v := range k.def.variants {
		for _, p := range v.payloads {
			// Outcome's Failure carries two String variants, which valueEqual
			// compares by tag and message as it compares the other payloads.
			failure := outcome && p.k.tag == tagNamed && p.k.def == namedPayloadDefs()[outcomeFailure]
			// A container payload compares structurally under both the
			// derived impl and rt.Equal: a container's `==` never
			// dispatches.
			// A Decimal payload compares with rt.EqDecimal under both: it is
			// the host body of std's `impl Equatable for Decimal`, and
			// rt.Equal calls it for a Decimal.
			if !irScalarLeafKind(p.k) && p.k != kindUnit && !failure && !irEqualityContainerKind(p.k) && !isDecimalKind(p.k) {
				return false
			}
		}
	}
	return true
}

// A local struct with scalar fields and no Equatable impl anywhere in the
// program, as noEquatableImpl establishes. Such a type compares structurally
// through valueEqual. An impl, derived or written, keeps the comparison on the
// impl route and outside this producer.
func (g *gen) irStructEqualityKind(k kind) bool {
	if k.tag != tagNamed || irParamShape(k) != ir.ValStruct || k.def.foreign != "" || !g.noEquatableImpl(k) {
		return false
	}
	for _, f := range k.def.fields {
		if !irScalarLeafKind(f.k) || f.k.def != nil {
			return false
		}
	}
	return true
}

// shortCircuit lowers `a and b` / `a or b` through the shared shape.
func (bl *irScalarBuilder) shortCircuit(t *ast.Binary) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	op := ir.LogicAnd
	if t.Op == "or" {
		op = ir.LogicOr
	}
	lhs, lk, _, ok := bl.lower(t.Left)
	if !ok || lk != kindBool {
		// `gen.logical` refuses a non-Bool operand as `logical operator on a
		// non-Bool` and the refusal is its own.
		return no()
	}
	opPos := bl.g.irPos(t.Line, t.Col)
	leftPos, rightPos := bl.g.irNodePos(t.Left), bl.g.irNodePos(t.Right)
	if op == ir.LogicOr {
		bl.record(t.Left, lhs, lk, true)
	} else if bl.recording > 0 {
		// `and`'s LEFT ROW, guarded by the left having DECIDED. The diamond
		// is ahead of the short-circuit so the shared shape is built
		// untouched; see the file header for why a diamond over the
		// recording changes nothing that is evaluated.
		decided := bl.f.NewBlock(leftPos, "and.leftdecided")
		carry := bl.f.NewBlock(opPos, "and.left")
		bl.b.SetTerm(ir.NewBranch(opPos, lhs, carry.ID(), decided.ID()))
		bl.b = decided
		bl.record(t.Left, lhs, lk, true)
		decided.SetTerm(ir.NewJump(opPos, carry.ID()))
		bl.b = carry
	}
	dst := bl.f.NewTemp()
	sc := ir.BeginShortCircuit(bl.f.Region, bl.b, op, opPos, leftPos, rightPos, dst, lhs)
	bl.b = sc.Rhs()
	rhs, rk, _, ok := bl.lower(t.Right)
	if !ok || rk != kindBool {
		return no()
	}
	// The right operand's row goes in the right-operand block, which is
	// exactly "only when the left did not decide", the condition the row is
	// recorded under. Before `Finish`; see the file header.
	bl.record(t.Right, rhs, rk, true)
	rightExit := bl.b
	bl.b = sc.Finish(rightExit, rightPos, rhs)
	bl.side(dst, irScalarSide{k: kindBool})
	// MOBILE: the destination is storage two Copies write and a reader reads,
	// and so pure and safe to read twice.
	return dst, kindBool, true, true
}

// rankedCompare lowers an ordering operator on a named type through its local
// `impl Comparable`, the first route for an ordering operator: the impl's compare
// answers an Ordering and the operator tests its rank.
func (bl *irScalarBuilder) rankedCompare(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	ord := stdEnumKind(stdEnumOrdering)
	impl := bl.g.implsByIface["Comparable"][k]
	if impl == nil {
		// The impl is in a sibling file (`impl Comparable for Widget` beside
		// Widget's declaration): its retained compare, as a sort takes it.
		sym := bl.siblingCompareSym(k)
		if sym == nil {
			return no()
		}
		call := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), sym, lhs, rhs)
		bl.b.Append(call)
		bl.side(call.Dst(), irScalarSide{k: ord, deferrable: true})
		c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, call.Dst())
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), kindBool, false, true
	}
	if !impl.lowerable {
		return no()
	}
	it := impl.items["compare"]
	if it == nil || !it.lowerable || len(it.params) != 2 || it.params[0] != k || it.params[1] != k || it.result != ord {
		return no()
	}
	call := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(it, "Comparable.compare"), lhs, rhs)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: ord, deferrable: true})
	c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, call.Dst())
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// comparableRankedCompare lowers `<`, `<=`, `>` or `>=` on a String, a Bool,
// a List or a Vector through the type's `Comparable.compare`:
// std's retained body for a String or a Bool, and List.compare /
// Vector.compare with the element comparator for a container. The operator
// tests the answer's rank.
func (bl *irScalarBuilder) comparableRankedCompare(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	call := &ast.Call{Line: t.Line, Col: t.Col, Args: []ast.Node{t.Left, t.Right}}
	args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
	var ordered ir.Temp
	switch {
	case k.tag == tagList:
		v, _, _, ok := bl.containerCompareAny(call, args, "List")
		if !ok {
			return no()
		}
		ordered = v
	case k.tag == tagNamed:
		v, _, _, ok := bl.containerCompareAny(call, args, "Vector")
		if !ok {
			return no()
		}
		ordered = v
	default:
		f := bl.g.stdCompareAt(k, stdEnumKind(stdEnumOrdering))
		if f == nil || f.irBody == nil {
			return no()
		}
		c := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), f.irBody.Sym(), lhs, rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: stdEnumKind(stdEnumOrdering), deferrable: true})
		ordered = c.Dst()
	}
	c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ordered)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// stdRankedCompare lowers an ordering operator on Decimal through std's
// `impl Comparable for Decimal`: the approved host
// `rt.DecimalCompare` answers an Ordering and the operator tests its rank.
func (bl *irScalarBuilder) stdRankedCompare(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	ord := stdEnumKind(stdEnumOrdering)
	if bl.g.implsByIface["Comparable"][k] != nil {
		return no()
	}
	f := bl.g.stdCompareAt(k, ord)
	if f == nil {
		return no()
	}
	call := &ast.Call{Line: t.Line, Col: t.Col, Args: []ast.Node{t.Left, t.Right}}
	args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
	p := bl.stdFuncPlan(call, args, f)
	if p == nil {
		return no()
	}
	v, _, _, ok := bl.qualEmit(call, args, p)
	if !ok {
		return no()
	}
	c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, v)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// stdEquality lowers `a == b` over a std named type through std's own
// `impl Equatable`: a call to its retained `equal?` body or approved host.
// `!=` does not take this route.
func (bl *irScalarBuilder) stdEquality(t *ast.Binary, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, bool) {
	if k.tag != tagNamed || k.def == nil || bl.g.implsByIface["Equatable"][k] != nil || bl.g.irStructEqualityKind(k) {
		return ir.NoTemp, false
	}
	f := bl.g.stdlibImplOf("Equatable.equal?", k, k, k)
	if f == nil || f.result != kindBool {
		return ir.NoTemp, false
	}
	// Inside that `equal?` itself (`impl Equatable for Byte { fn equal?(a, b)
	// { a == b } }`) the operator is the primitive comparison the impl is
	// defined by, and routing it back to the impl recurses forever.
	if f.decl != nil && bl.f.Sym() == bl.g.irCalleeSym(f.decl, f.decl.Name) {
		return ir.NoTemp, false
	}
	call := &ast.Call{Line: t.Line, Col: t.Col, Args: []ast.Node{t.Left, t.Right}}
	args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
	p := bl.stdFuncPlan(call, args, f)
	if p == nil {
		return ir.NoTemp, false
	}
	v, _, _, ok := bl.qualEmit(call, args, p)
	return v, ok
}
