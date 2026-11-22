package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A qualified call of an operator interface's function (`Equatable.equal(a,
// b)`, `Int.add(a, b)`, `Hashable.hash(x)`, `Comparable.compare(a, b)`,
// `Point.equal(p, q)`) is lowered by one switch on the checker's record
// of how the call is answered (analysis.Callee.Form, at the instance's
// types: calleeAt), interfaceCall:
//
//   - by an impl the machine computes (machineImpls, read through the
//     operator record the call's record is: operatorUseOfCall), as the
//     operator the impl is: `==` (equality), an arithmetic, bitwise or
//     shift operator (arithmetic), `~~~` (bitwiseNot), each with the
//     call's record carried onto it. `a == b` and `Equatable.equal(a, b)`
//     end in one arm. Any other impl is the call of it the record names,
//     which the routes after the switch plan (recordedCallPlan).
//   - by shape: Equatable's as `==` by shape (equality), Hashable's as a
//     tuple's or a record's mix (tupleHashCall) or the hash a Map key of
//     the type takes (irHashValueHost), Comparable's as a tuple's
//     lexicographic compare (tupleCompareCall).
//   - by dispatch: Equatable's as `==` over two interface values
//     (equality), Hashable's as the Map key hash of the held value, or,
//     on a value held as std's Hashable itself, the dispatched call the
//     routes after plan (dispatchedCallPlan).
//
// A call with no record the switch reads (none, or a per-instance record
// its instance does not resolve) declines, counted in the operator census
// (noteUnreadCall) as the operators' are. A form an interface's arm has
// no shape for (an arithmetic function by shape) declines as an operator
// arm declines over operands it cannot lower.

// operatorIface is what interfaceCall knows of an operator interface's
// function: its interface, and the operator it is (op, of the family
// whose switch lowers that operator), none for one no operator spells
// (Hashable's hash, Comparable's compare, which answers an Ordering).
type operatorIface struct {
	iface  analysis.StdType
	family operatorFamily
	op     string
}

// operatorIfaces are the functions interfaceCall lowers the calls of, by
// identity (analysis.FileAnalysis.CalleeInterfaceFunc): Equatable's
// equal, Hashable's hash, Comparable's compare, and the one function of
// each arithmetic, bitwise and shift interface of operIfaceSpecs. Another
// function of one of these interfaces (Comparable's min, a default) is
// no operator's.
var operatorIfaces = func() map[analysis.StdFunc]operatorIface {
	out := map[analysis.StdFunc]operatorIface{
		stdEquatable.Func("equal"):    {iface: stdEquatable, family: familyEquality, op: "=="},
		stdHashable.Func("hash"):      {iface: stdHashable},
		stdComparable.Func("compare"): {iface: stdComparable},
	}
	for _, s := range operIfaceSpecs {
		id := analysis.StdType{Module: s.module, Name: s.nomi}
		switch {
		case s.unary:
			out[id.Func(s.method)] = operatorIface{iface: id, family: familyBitwiseNot, op: "~~~"}
		case s.op != "":
			out[id.Func(s.method)] = operatorIface{iface: id, family: familyArithmetic, op: s.op}
		}
	}
	return out
}()

// interfaceCall lowers t, a qualified call of an operator interface's
// function, by the form its record answers it with. handled is false for
// a call of any other function, and for one an impl answers that the
// machine does not compute, or that dispatches on a value held as std's
// Hashable itself: the routes after plan those from the record.
func (bl *irScalarBuilder) interfaceCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	c := bl.g.calleeAt(t)
	oi, operator := operatorIfaces[bl.g.calleeInterfaceFunc(c)]
	if !operator {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch c.Form {
	case analysis.OperatorByImpl:
		use := operatorUseOfCall(oi.iface, c, bl.g.firstOperandType(t))
		if oi.family == "" || bl.g.machineRouteOf(use) == routeUnset {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		v, k, mobile, ok = bl.asOperator(t, oi, use)
		return v, k, mobile, ok, true
	case analysis.OperatorStructural:
		switch oi.iface {
		case stdEquatable:
			v, k, mobile, ok = bl.asOperator(t, oi, &analysis.OperatorUse{Form: c.Form})
			return v, k, mobile, ok, true
		case stdHashable:
			if v, k, mobile, ok, handled := bl.tupleHashCall(t); handled {
				return v, k, mobile, ok, true
			}
			v, ok = bl.hashValue(t)
			return v, kindInt, false, ok, true
		case stdComparable:
			v, k, mobile, ok, _ = bl.tupleCompareCall(t)
			return v, k, mobile, ok, true
		}
	case analysis.OperatorDispatched:
		switch oi.iface {
		case stdEquatable:
			v, k, mobile, ok = bl.asOperator(t, oi, &analysis.OperatorUse{Form: c.Form})
			return v, k, mobile, ok, true
		case stdHashable:
			if heldAsStd(c.For, stdHashable) {
				// A value held as std's Hashable itself dispatches to its
				// type's impl through the table (stdprogiface.go), so the
				// call answers what `Hashable.hash` of the concrete value
				// answers.
				return ir.NoTemp, kindInvalid, false, false, false
			}
			v, ok = bl.hashValue(t)
			return v, kindInt, false, ok, true
		case stdComparable:
			// Values of a type parameter an abstract body leaves open,
			// held as its Comparable bound: a call through the bound
			// (boundComparePlan).
			if p := bl.g.boundComparePlan(); p != nil {
				if args := bl.irQualLowerArgs(t); args.ok {
					v, k, mobile, ok = bl.qualEmit(t, args, p)
					return v, k, mobile, ok, true
				}
			}
		}
	}
	bl.g.noteUnreadCall(t, c)
	return ir.NoTemp, kindInvalid, false, false, true
}

// calleeInterfaceFunc is the std interface's function the call record c
// reaches (analysis.FileAnalysis.CalleeInterfaceFunc), looked up in the
// file whose check recorded the call and, for a record answered at an
// instance, in the file whose program the instance's types are
// (instanceFA), as operatorImplStd looks up an operator's impl.
func (g *gen) calleeInterfaceFunc(c analysis.Callee) analysis.StdFunc {
	src := g.srcFA()
	if src == nil {
		return analysis.StdFunc{}
	}
	if s := src.CalleeInterfaceFunc(c); !s.IsZero() {
		return s
	}
	if fa := g.instanceFA(); fa != src {
		return fa.CalleeInterfaceFunc(c)
	}
	return analysis.StdFunc{}
}

// heldAsStd reports whether t, the checker's type of a dispatched call's
// receiver, is std's interface iface itself.
func heldAsStd(t analysis.Type, iface analysis.StdType) bool {
	it, ok := analysis.ResolveTypeVar(t).(*analysis.InterfaceType)
	return ok && it.Origin == iface.Module && it.Name == iface.Name
}

// asOperator lowers t, a call of oi's function, as the operator the
// function is, carrying use, the call's record as the operator's
// (operatorUseOfCall), onto the operator it writes: `==` as the written
// operator is lowered (compare), its operands with it, so the two
// operands are made one kind as `==`'s are (`Maybe.equal(Some(circle),
// Some(shape))` widens the Circle into Shape); `~~~` to bitwiseNot and
// any other to arithmetic, over the call's lowered operands. An operand
// that does not lower declines.
func (bl *irScalarBuilder) asOperator(t *ast.Call, oi operatorIface, use *analysis.OperatorUse) (ir.Temp, kind, bool, bool) {
	if oi.family == familyEquality {
		if len(t.Args) != 2 {
			panic(fmt.Sprintf("irbuild: line %d: a call of Equatable's equal with %d operands, where it takes 2", t.Line, len(t.Args)))
		}
		at := &ast.Binary{Op: oi.op, Left: argExpr(t.Args[0]), Right: argExpr(t.Args[1]), Line: t.Line, Col: t.Col}
		defer bl.g.carryUse(at, use)()
		return bl.compare(at, ir.OpEq)
	}
	args := bl.qualCallArgs(t)
	if !args.ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if oi.family == familyBitwiseNot {
		operatorArity(t, args, 1)
		at := &ast.Unary{Op: oi.op, Right: argExpr(t.Args[0]), Line: t.Line, Col: t.Col}
		defer bl.g.carryUse(at, use)()
		return bl.bitwiseNot(at, args.temps[0], args.kinds[0])
	}
	operatorArity(t, args, 2)
	at := &ast.Binary{Op: oi.op, Left: argExpr(t.Args[0]), Right: argExpr(t.Args[1]), Line: t.Line, Col: t.Col}
	defer bl.g.carryUse(at, use)()
	return bl.arithmetic(at, irArithOps[oi.op], args.temps[0], args.temps[1], args.kinds[0], args.kinds[1])
}

// operatorArity panics unless args, the lowered operands of t, a call of an
// operator interface's function, are n: the function's parameters, which
// have no defaults, and the checker checks the call's arity against them.
func operatorArity(t *ast.Call, args irQualArgs, n int) {
	if len(args.kinds) != n {
		panic(fmt.Sprintf("irbuild: line %d: a call of an operator interface's function with %d operands, where it takes %d", t.Line, len(args.kinds), n))
	}
}

// irHashValueHost is the VM intrinsic that hashes a value as a Map key or
// Set element of its type hashes it (vm/keys.go): an interface value by
// the held type's hand-written Hashable when it has one, by its name when
// only its Equatable is hand-written, structurally otherwise; any other
// value structurally. Two values `==` calls equal hash alike.
const irHashValueHost = "vm.hash_value"

// hashValue lowers t, a call of `Hashable.hash(x)` answered by shape or by
// dispatch, as the irHashValueHost call over its one operand.
func (bl *irScalarBuilder) hashValue(t *ast.Call) (ir.Temp, bool) {
	args := bl.qualCallArgs(t)
	if !args.ok {
		return ir.NoTemp, false
	}
	operatorArity(t, args, 1)
	pos := bl.g.irNodePos(t)
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(irHashValueHost, irHashValueHost), args.temps[0])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindInt})
	return c.Dst(), true
}

// qualCallArgs lowers the operands of the qualified call t
// (irQualLowerArgs), each payload-free prelude variant written bare typed
// by the parameter the checker solved for it (checkedBareOperands).
func (bl *irScalarBuilder) qualCallArgs(t *ast.Call) irQualArgs {
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	return bl.irQualLowerArgs(t)
}
