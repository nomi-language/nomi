package irbuild

// Comparisons and short-circuits in retained functions and test bodies.
//
// # The comparison
//
// `ir.Compare` is the node and internal/ir/compare.go argues its existence.
// The producer's job is to establish what that node's constructor cannot: that
// both operands are the SAME shape. Mixed kinds decline. `==` and `!=` then
// take the arm of their record's form (irequality.go); an ordering compares
// two Ints, Floats or Bools natively, a tuple part by part, and anything else
// through its `impl Comparable`.
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
	"github.com/nomi-language/nomi/internal/analysis"
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
		// `None == None`: neither side has an expected type, so the
		// instance the checker records for the left types both. Two
		// constants; no effect moves.
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
	wantL, widenL, wantR, widenR := bl.embedsWidenedOperand(t, op)
	var lhs ir.Temp
	var lk kind
	var mobile, ok bool
	if widenL {
		lhs, lk, mobile, ok = bl.lowerWant(t.Left, wantL)
	} else {
		lhs, lk, mobile, ok = bl.lowerComparisonOperand(t.Left)
	}
	if !ok {
		return no()
	}
	if !mobile {
		// An impure left operand is forced into a temporary so a later
		// operand's instructions cannot change the value it holds. `bl.binary`'s
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
	if widenR {
		rhs, rk, rmobile, ok = bl.lowerWant(t.Right, wantR)
	} else if bl.bareEqualityOperand(t.Right, op) {
		// `x == None`: the bare variant takes the other operand's type, as an
		// argument or a return takes its parameter's.
		rhs, rk, rmobile, ok = bl.preludeBareValue(t.Right, lk)
	} else if c, isCall := t.Right.(*ast.Call); isCall && !op.Ordering() && lk.tag == tagNamed && lk.def != nil && lk.def.preludeOf != nil {
		// `x == Option.Some(v)`: the checker solved both operands as one
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
		rhs, rk, rmobile, ok = bl.lowerComparisonOperand(t.Right)
	}
	if !ok {
		return no()
	}
	return bl.compareOperands(t, op, lhs, lk, rhs, rk, rmobile)
}

// lowerComparisonOperand lowers a comparison operand. An unnamed bracket
// literal is the collection its position expects, and the checker types it
// against the other operand (analysis.checkComparisonOperands): in
// `[s, Shape.Dot] == [c, c]`, with `c: Circle` and `s: Shape`, the right
// literal is a List<Shape>. It is lowered at that type, so its items widen
// where they sit, as they do under an annotation.
func (bl *irScalarBuilder) lowerComparisonOperand(n ast.Node) (ir.Temp, kind, bool, bool) {
	if lit, isList := n.(*ast.ListLit); isList && lit.TypeName == nil {
		if k := bl.g.checkedKind(n); k.tag != tagInvalid {
			return bl.lowerWant(n, k)
		}
	}
	return bl.lower(n)
}

// embedsWidenedOperand says which operand of an equality is lowered at the
// other's type, when the checker typed the two apart only by an embedded
// value inside a generic type: `Some(circle) == Some(Shape.Dot)` compares an
// Option<Circle> with an Option<Shape>. The operand whose type is assignable to
// the other's (TypeAssignable) is the narrower, and it is lowered at the
// wider type, so its embedded values widen where they sit, as they do where
// an Option<Shape> is expected. The two values then have one runtime shape. A
// value of the embedded type itself beside its enum (`circle == shape`) is
// left to compareOperands, which widens it after lowering.
func (bl *irScalarBuilder) embedsWidenedOperand(t *ast.Binary, op ir.CompareOp) (wantL kind, widenL bool, wantR kind, widenR bool) {
	if op.Ordering() {
		return kindInvalid, false, kindInvalid, false
	}
	tl, tr := bl.g.checkedExprType(t.Left), bl.g.checkedExprType(t.Right)
	if tl == nil || tr == nil || analysis.TypesIdentical(tl, tr) {
		return kindInvalid, false, kindInvalid, false
	}
	l, r := bl.g.project(tl), bl.g.project(tr)
	if l == r || l.tag == tagInvalid || r.tag == tagInvalid || irEmbeddedIn(l, r) || irEmbeddedIn(r, l) {
		return kindInvalid, false, kindInvalid, false
	}
	switch {
	case analysis.TypeAssignable(tr, tl):
		return r, true, kindInvalid, false
	case analysis.TypeAssignable(tl, tr):
		return kindInvalid, false, l, true
	}
	return kindInvalid, false, kindInvalid, false
}

// bareEqualityOperand reports an equality operand that is a bare payload-free
// prelude variant (`None`), which has no type of its own.
func (bl *irScalarBuilder) bareEqualityOperand(n ast.Node, op ir.CompareOp) bool {
	if op.Ordering() {
		return false
	}
	name, line, col, ok := preludeValueName(n)
	if !ok {
		return false
	}
	// `Direction.North` has the same spelling as `Option.None`; only a
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

// compareOperands is `compare` once both operands are lowered: an
// equality by its record (equality), an ordering by its record
// (ordering).
func (bl *irScalarBuilder) compareOperands(t *ast.Binary, op ir.CompareOp, lhs ir.Temp, lk kind,
	rhs ir.Temp, rk kind, rmobile bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	ok := true
	if !op.Ordering() {
		if irEmbeddedIn(rk, lk) {
			// `circle == shape`: the embedded value widens into its
			// `embeds` variant, as it does entering a Shape position.
			lhs, lk, ok = bl.convertTo(t.Left, lhs, lk, rk)
		} else if irEmbeddedIn(lk, rk) {
			rhs, rk, ok = bl.convertTo(t.Right, rhs, rk, lk)
		} else if irHeldKind(lk) && !irHeldKind(rk) {
			// `t == (1, "a")` with `t: Equatable`: the concrete value is
			// boxed as the interface value it is compared with, and the two
			// compare by their run-time types below (spec §12 *Interface
			// values*).
			rhs, rk, ok = bl.convertTo(t.Right, rhs, rk, lk)
		} else if irHeldKind(rk) && !irHeldKind(lk) {
			lhs, lk, ok = bl.convertTo(t.Left, lhs, lk, rk)
		}
	}
	if !ok || lk != rk {
		return no()
	}
	if bl.recording > 0 && !rmobile {
		// Inside a recording subject the right operand is read twice, once
		// by the record and once by the comparison, so an impure one is
		// forced into a temporary first. Outside a recording subject
		// `bl.record` is a no-op, so no copy is needed.
		c := ir.NewCopy(bl.g.irNodePos(t.Right), bl.f.NewTemp(), rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: rk, copy: irCopyForce})
		rhs, rmobile = c.Dst(), true
	}
	// The rows, in source order: both operands of `== != < > <= >=` are
	// worth showing, and the redundancy rule that drops `1 = 1` is rt's to
	// apply. Recorded before any route is chosen, so a comparison answered
	// by an impl call shows its operands too. The `==` that an assertion
	// subject's `Option.equal(a, b)` is lowered as (asOperator), whose
	// operands are the call's own nodes, hands them to the call instead,
	// which records them as its rows (recordedQualCall).
	if c := bl.qualRecord; c != nil && len(c.Args) == 2 && t.Left == argExpr(c.Args[0]) && t.Right == argExpr(c.Args[1]) {
		bl.qualRecordArgs = irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{lk, rk}, mobile: []bool{true, true}, ok: true}
		bl.qualRecordSeen = true
	} else {
		bl.record(t.Left, lhs, lk, true)
		bl.record(t.Right, rhs, rk, true)
	}
	// The result is not mobile, as `bl.binary`'s arithmetic result is
	// not: a comparison in a left-operand position is forced into a Copy.
	if op.Ordering() {
		v, ok := bl.ordering(t, op, lhs, rhs, lk, rmobile)
		if !ok {
			return no()
		}
		return v, kindBool, false, true
	}
	v, ok := bl.equality(t, op, lhs, rhs, lk, rmobile)
	if !ok {
		return no()
	}
	return v, kindBool, false, true
}

// ordering lowers `<`, `<=`, `>` or `>=` over two lowered operands of
// kind k by the form the checker recorded for the operator t, a by-impl
// record split by the route machineImpls gives its impl: an impl the
// machine computes natively (Int's, Float's and Bool's compare), one
// ir.Compare of the scalar; by shape, a tuple's lexicographic compare
// (tupleRankedCompare); any other impl, a call of the compare the record
// names and a test of the rank of the Ordering it answers
// (rankedCompare). An ordering over interface values, by dispatch, has
// no arm: the checker rejects one written over them, and a generic call
// that would instantiate one at an interface type, since an interface type
// meets no Comparable bound (spec §12 *Interface values*). It reaches
// here only in the instance of a generic subtype's `try_from` that the
// checker types at its bounds' interfaces for the rule's effect walk
// (analysis.SubtypeRuleCheck), which is never run, and declines. An arm
// that cannot lower its operands, and an operator with no record an arm
// reads, decline here.
func (bl *irScalarBuilder) ordering(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, bool) {
	use := bl.g.operatorUse(t)
	var form analysis.OperatorForm
	if use != nil {
		form = use.Form
	}
	machine := bl.g.machineRouteOf(use)
	switch {
	case machine == routeNative && (k == kindInt || k == kindFloat || k == kindBool):
		c := ir.NewCompare(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, irParamShape(k), lhs, rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), true
	case form == analysis.OperatorStructural && k.tag == tagTuple:
		return bl.tupleRankedCompare(t, op, lhs, rhs, k)
	case form == analysis.OperatorByImpl && machine == routeUnset && use.Impl != nil:
		if v, ok := bl.rankedCompare(t, op, lhs, rhs, k, rmobile); ok {
			return v, true
		}
	case form == analysis.OperatorDispatched:
		// Values of a type parameter an abstract body leaves open, held as
		// its Comparable bound: a call through the bound of compare, and a
		// test of its rank (boundcall.go).
		if p := bl.g.boundComparePlan(); p != nil {
			call, done := bl.operatorCall(t)
			defer done()
			args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
			if ordered, ok := bl.rankedCall(call, args, p); ok {
				c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ordered)
				bl.b.Append(c)
				bl.side(c.Dst(), irScalarSide{k: kindBool})
				return c.Dst(), true
			}
		}
	}
	bl.g.noteUnreadOperator(t, familyOrdering)
	return ir.NoTemp, false
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
	if _, _, leaves := bl.g.exitingOperand(t.Right); leaves {
		// `is_ready or { return Err(e) }`, `x > 0 and f(case x { 1 -> return
		// True  _ -> return False })`: the right operand leaves the body
		// when it runs, so the operation is `if ready? { True } else {
		// <right> }` (`and`: `if x { <right> } else { False }`), the right
		// operand an arm into dst. Where it leaves, the answer is the
		// left's, which the entry already copied into dst.
		join := sc.Branched()
		outer := bl.sh.result
		bl.sh.result = dst
		rk, ok := bl.armInto(sc.Rhs(), join, t.Right, irFuncSig{result: kindBool})
		bl.sh.result = outer
		if !ok || (rk != kindBool && rk != kindDiverged) {
			return no()
		}
		bl.b = join
		bl.side(dst, irScalarSide{k: kindBool})
		return dst, kindBool, true, true
	}
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
	// Mobile: the destination is storage two Copies write, and reading it
	// has no effect, so it is safe to read twice.
	return dst, kindBool, true, true
}

// rankedCompare lowers `<`, `<=`, `>` or `>=` through an `impl
// Comparable` and tests the rank of the Ordering its compare answers. The
// impl is the one the operator's record names, called as that record's
// call (operatorCall): a List's or a Vector's, std's own, through
// containerCompareAny, the machine's lexicographic intrinsic over the
// element type's compare, else std's instance, as `List.compare(a, b)`
// is; any other, a program's in this file or another and std's (a
// Decimal's host, a String's or a Date's body), planned by
// recordedCallPlan.
func (bl *irScalarBuilder) rankedCompare(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind, rmobile bool) (ir.Temp, bool) {
	call, done := bl.operatorCall(t)
	defer done()
	args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{k, k}, mobile: []bool{true, rmobile}, ok: true}
	var ordered ir.Temp
	var ok bool
	if owner := containerCompareOwner(k); owner != "" {
		ordered, _, _, ok = bl.containerCompareAny(call, args, owner)
	} else {
		ordered, ok = bl.rankedCall(call, args, bl.recordedCallPlan(call, args, "Comparable.compare"))
	}
	if !ok {
		return ir.NoTemp, false
	}
	c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ordered)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), true
}

// containerCompareOwner is the std container whose own `impl
// Comparable` orders values of kind k, List or Vector, which
// containerCompareAny lowers; "" for any other kind. Neither container
// takes another impl of Comparable.
func containerCompareOwner(k kind) string {
	if k.tag == tagList {
		return "List"
	}
	if _, isVector := vectorElem(k); isVector {
		return "Vector"
	}
	return ""
}

// rankedCall appends the call of compare that p plans for the operator's
// call, and answers the Ordering it returns; false when p is nil or
// answers no Ordering.
func (bl *irScalarBuilder) rankedCall(call *ast.Call, args irQualArgs, p *irQualPlan) (ir.Temp, bool) {
	if p == nil || p.result != stdEnumKind(stdEnumOrdering) {
		return ir.NoTemp, false
	}
	v, _, _, ok := bl.qualEmit(call, args, p)
	return v, ok
}
