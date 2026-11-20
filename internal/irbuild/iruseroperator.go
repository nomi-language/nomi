package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// arithmetic lowers the arithmetic operator t (`+`, `-`, `*`, `/`, `%`,
// a bitwise or a shift operator) over its two lowered operands, of kinds
// lk and rk, by the form the checker recorded for it, a by-impl record
// split by the route machineImpls gives its impl: an impl the machine
// computes natively, the machine's operation (nativeArithmetic); any
// other impl, a call of the one the record names (implArithmetic). No
// arithmetic operator is answered by shape or by dispatch. An arm that
// cannot lower its operands, and an operator with no record an arm reads,
// decline here.
func (bl *irScalarBuilder) arithmetic(t *ast.Binary, op ir.ArithOp, lhs, rhs ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	use := bl.g.operatorUse(t)
	machine := bl.g.machineRouteOf(use)
	switch {
	case machine == routeNative:
		if v, k, mobile, ok := bl.nativeArithmetic(t, op, lhs, rhs, lk, rk); ok {
			return v, k, mobile, true
		}
	case machine == routeUnset && use != nil && use.Form == analysis.OperatorByImpl && use.Impl != nil:
		if v, k, mobile, ok := bl.implArithmetic(t, lhs, rhs, lk, rk); ok {
			return v, k, mobile, true
		}
	}
	bl.g.noteUnreadOperator(t, familyArithmetic)
	return ir.NoTemp, kindInvalid, false, false
}

// nativeArithmetic is the machine's operation for the arithmetic operator
// t, whose record names an impl the machine computes itself
// (machineImpls): List's `+`, the concatenation `List.concat` is the host
// operation of, over operands made one List kind; String's `+`,
// ir.Concat; Int's, Float's and Decimal's operators, ir.Arith. The kinds
// choose only the operation's IR shape.
func (bl *irScalarBuilder) nativeArithmetic(t *ast.Binary, op ir.ArithOp, lhs, rhs ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.Op == "+" && (lk.tag == tagList || rk.tag == tagList) {
		call := &ast.Call{Line: t.Line, Col: t.Col, Args: []ast.Node{t.Left, t.Right}}
		args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{lk, rk}, mobile: []bool{true, true}, ok: true}
		p := bl.listCallPlan(call, args, "concat")
		if p == nil {
			return no()
		}
		return bl.qualEmit(call, args, p)
	}
	if lk != rk {
		panic("irbuild: nativeArithmetic: the record names an operator impl machineImpls computes natively, over operands of two kinds")
	}
	if lk == kindString {
		// The result is not mobile, as an arithmetic result is not.
		c := ir.NewConcat(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), lhs, rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindString})
		return c.Dst(), kindString, false, true
	}
	ak, modelled := bl.g.irArithKind(op, lk, t.Wrapping)
	if !modelled {
		return no()
	}
	a := ir.NewArith(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ak, lhs, rhs)
	bl.b.Append(a)
	// An arithmetic result is not mobile, so one in a left operand position
	// is always forced into a Copy.
	return a.Dst(), lk, false, true
}

// implArithmetic calls the impl of the arithmetic operator t its record
// names (recordedOperatorImpl), over operands of kinds lk and rk: a
// program's impl item (userOperatorCall), std's function of the record's
// identity (stdOperatorCall), or std's generic impl of a Set, a Map or a
// Vector, instantiated at the receiver's element type
// (stdGenericOperatorCall).
func (bl *irScalarBuilder) implArithmetic(t *ast.Binary, lhs, rhs ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	og, it, f := bl.recordedOperatorImpl(t, lk)
	if it != nil {
		params, result, ok := bl.g.itemKinds(og, it)
		if !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
		return bl.userOperatorCall(t, operIfaceByOp[t.Op], og, it, params, result, lhs, rhs, lk, rk)
	}
	if stdOperatorFits(f, lk, rk) {
		return bl.stdOperatorCall(t, f, lhs, rhs)
	}
	if v, k, mobile, ok, handled := bl.stdGenericOperatorCall(t, lhs, rhs, lk, rk); handled {
		return v, k, mobile, ok
	}
	return ir.NoTemp, kindInvalid, false, false
}

// userOperatorCall lowers `a op b`, whose operator interface is spec, as
// a call of the impl function og's item it, the one the checker recorded
// for the operator (recordedOperatorImpl), whose parameter and result
// kinds as this gen names them are params and result. Exact parameter
// kinds need no coercion instructions; wider signatures are declined.
func (bl *irScalarBuilder) userOperatorCall(at *ast.Binary, spec *operIfaceSpec, og *gen, it *implItem, params []kind, result kind, left, right ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	if spec == nil || it == nil || it.impl == nil || !it.impl.lowerable || !it.lowerable || len(params) != 2 || len(it.params0) != 2 || params[0] != lk || params[1] != rk || !irCallableValueKind(result) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), irCallSite(at), og.irImplSym(it), left, right)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result, deferrable: true})
	return n.Dst(), result, false, true
}

// bitwiseNot lowers `~~~operand` by the form the checker recorded for
// it: Int's, the impl the machine computes itself (machineImpls), is
// `ir.OpNot`, and any other type's a call of the BitwiseNot impl the
// record names (bitwiseNotImpl). An operand with no record an arm reads
// declines here.
func (bl *irScalarBuilder) bitwiseNot(t *ast.Unary, src ir.Temp, k kind) (ir.Temp, kind, bool, bool) {
	use := bl.g.operatorUse(t)
	machine := bl.g.machineRouteOf(use)
	switch {
	case machine == routeNative:
		ak, modelled := bl.g.irArithKind(ir.OpNot, k, false)
		if !modelled {
			panic("irbuild: bitwiseNot: the record names a BitwiseNot impl machineImpls computes natively, over an operand the machine has no `~~~` for")
		}
		a := ir.NewUnaryArith(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), ir.OpNot, ak, src)
		bl.b.Append(a)
		return a.Dst(), k, false, true
	case machine == routeUnset && use != nil && use.Form == analysis.OperatorByImpl && use.Impl != nil:
		if v, rk, ok := bl.bitwiseNotImpl(t, src, k); ok {
			return v, rk, false, true
		}
	}
	bl.g.noteUnreadOperator(t, familyBitwiseNot)
	return ir.NoTemp, kindInvalid, false, false
}

// bitwiseNotImpl calls the BitwiseNot impl the record of `~~~operand`
// names (recordedOperatorImpl) over src, of kind k: std's function of
// the record's identity (Byte's), or the program's impl item.
func (bl *irScalarBuilder) bitwiseNotImpl(t *ast.Unary, src ir.Temp, k kind) (ir.Temp, kind, bool) {
	og, it, f := bl.recordedOperatorImpl(t, k)
	if f != nil && f.why == "" && sameParamKinds(f.params, []kind{k}) {
		if f.canon != nil {
			f = f.canon
		}
		if f.irBody == nil || f.rtCall != "" {
			return ir.NoTemp, kindInvalid, false
		}
		// A lowered body's signature is held.
		irHeldOperand(f.result, "a BitwiseNot impl's result")
		n := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), f.irBody.Sym(), src)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: f.result, deferrable: true})
		return n.Dst(), f.result, true
	}
	if it == nil {
		return ir.NoTemp, kindInvalid, false
	}
	params, result, ok := bl.g.itemKinds(og, it)
	if !ok || !it.impl.lowerable || !it.lowerable || len(params) != 1 || params[0] != k || !irCallableValueKind(result) {
		return ir.NoTemp, kindInvalid, false
	}
	n := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), og.irImplSym(it), src)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result, deferrable: true})
	return n.Dst(), result, true
}
