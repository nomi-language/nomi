package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) stdOperatorCall(at *ast.Binary, f *stdFunc, left, right ir.Temp) (ir.Temp, kind, bool, bool) {
	if f.canon != nil {
		f = f.canon
	}
	if f.irBody == nil || f.rtCall != "" || (!irCallableValueKind(f.result) && f.result != kindUnit) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), irCallSite(at), f.irBody.Sym(), left, right)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: f.result, deferrable: true})
	return n.Dst(), f.result, false, true
}

// stdGenericOperatorCall lowers `a + b` whose left operand is a generic std
// container with an operator impl (`impl Add<Set<T>, Set<T>> for Set<T>`,
// `impl Add<Vector<T>, Vector<T>> for Vector<T>`): the impl's body is
// instantiated at the receiver's element type, exactly as the
// interface-qualified `Add.add(a, b)` is. handled is false when the left
// operand is not such a container, or is a List, whose `+` is
// `List.concat`'s intrinsic.
func (bl *irScalarBuilder) stdGenericOperatorCall(at *ast.Binary, left, right ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool, bool) {
	spec, found := operIfaceByOp[at.Op]
	if !found || lk.tag == tagList {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	base := irContainerBaseName(lk)
	if base == "" || bl.g.stdGenericTemplate(base, spec.method) == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	// The call `Add.add(left, right)` the operator means, over operands
	// already lowered in order (stdInstPreCall), as the List arm spells
	// `List.concat`.
	call := &ast.Call{Line: at.Line, Col: at.Col, Args: []ast.Node{at.Left, at.Right}}
	args := irQualArgs{temps: []ir.Temp{left, right}, kinds: []kind{lk, rk}, mobile: []bool{true, true}, ok: true}
	return bl.ifaceContainerCall(call, args, spec.nomi, spec.method)
}
