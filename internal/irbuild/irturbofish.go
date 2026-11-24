package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// turbofishOnInterface reports whether t is `Iface.method<T>(args)`: a
// turbofish on a function the interface declares, whose type argument is
// the implementor. An owner function in an `impl Iface { ... }` block
// (`Iter.sum<Int>(xs)`) is generic in its own type parameters, which the
// turbofish gives as any generic function's does.
func (bl *irScalarBuilder) turbofishOnInterface(t *ast.Call) bool {
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil || bl.g.fa == nil {
		return false
	}
	// What the checker resolved the qualifier to, however it is spelled:
	// `FromJson`, or `json.FromJson` through its module, as a derived
	// FromJson names std's through the hidden `json$` import.
	c := bl.g.srcFA().CalleeOf(t.Func)
	if c.Qualifier.Kind != analysis.QualInterface {
		return false
	}
	if c.Ref == nil {
		return true
	}
	_, declared := c.Ref.Node.(*ast.InterfaceMethod)
	return declared
}

// turbofishDispatch lowers `Iface.method<T>(args)` as the call at T, the
// first type argument, the implementor, which is what selects the impl
// (`FromJson.from_json<Int>(json)` declares Self only in its result): a
// scalar's or a declared type's impl, or a generic std container's
// instantiated at the argument's components (stdKindQualCall). At a
// scalar, handled is false: the call is lowered as written, planned from
// its record, which names the scalar's impl. A type argument none of
// those answers declines. The function's own type arguments, after T, are
// in the record the plan reads. The caller has asked turbofishOnInterface
// of a call with type arguments.
func (bl *irScalarBuilder) turbofishDispatch(t *ast.Call) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, true }
	fa := t.Func.(*ast.FieldAccess)
	// kindInvalid: lookup — an argument with no kind declines.
	if k := bl.g.typeOf(t.TypeArgs[0]); k != kindInvalid {
		// The checker's type of the argument is the type the call is for.
		if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, bl.g.checkerTypeArg(bl.g.calleeOf(t).For), fa.Field.Name); handled {
			return v, rk, mobile, ok, true
		}
		if irScalarLeafKind(k) {
			return ir.NoTemp, kindInvalid, false, false, false
		}
	}
	return no()
}

// returnSelfDispatch lowers `Iface.method(args)` whose method declares self
// only in its RETURN and whose call names no type argument:
// `notes: List<Note> = FromJson.from_json(json)?`. No argument carries the
// receiver, so the implementation is the one the checker solved self to from
// the result's expected type (analysis.Symbol.ReturnSelf), dispatched as the
// turbofish spelling of the same type would be. handled is false for every
// other call.
func (bl *irScalarBuilder) returnSelfDispatch(t *ast.Call) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil || len(t.TypeArgs) != 0 || bl.g.fa == nil {
		return no()
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType {
		return no()
	}
	// The interface spelling only: stdKindQualCall lowers the call at the
	// solved type, and a call qualified with that type does not come back
	// here.
	owner := bl.g.srcFA().References[analysis.Pos{Line: ti.Line, Col: ti.Col}]
	if owner == nil || owner.Kind != analysis.SymbolInterface || owner.Name != ti.Name {
		return no()
	}
	ref := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if ref == nil || ref.ReturnSelf == nil {
		return no()
	}
	im, isMethod := ref.Node.(*ast.InterfaceMethod)
	if !isMethod || selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt() >= 0 {
		return no()
	}
	k := bl.g.project(ref.ReturnSelf)
	// kindInvalid: lookup — a self with no representation falls through to the ordinary qualified-call routes, which report it.
	if k == kindInvalid {
		return no()
	}
	return bl.stdKindQualCall(t, k, bl.g.checkerTypeArg(ref.ReturnSelf), fa.Field.Name)
}
