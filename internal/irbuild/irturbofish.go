package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// turbofishDispatch lowers `Iface.method<T>(args)` whose type argument names
// a concrete, non-generic type as the type-qualified `T.method(args)`: the
// type argument is what selects the impl (`FromJson.from_json<Int>(json)`
// declares Self only in its result), and naming T as the owner selects the
// same one. A generic type argument (`List<Int>`) needs an instance of a
// generic impl and declines.
func (bl *irScalarBuilder) turbofishDispatch(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.TypeArgs) != 1 {
		return no()
	}
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil {
		return no()
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType || bl.g.fa == nil {
		return no()
	}
	sym, found := bl.g.fa.References[analysis.Pos{Line: ti.Line, Col: ti.Col}]
	if !found || sym.Kind != analysis.SymbolInterface {
		return no()
	}
	// kindInvalid: lookup — an argument with no kind falls through to the spelled-owner route below.
	if k := bl.g.typeOf(t.TypeArgs[0]); k != kindInvalid {
		// The type argument's own impl: a scalar or a local type spelled as
		// its type-qualified call, or a generic std container's impl
		// instantiated at the argument's components.
		if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, fa.Field.Name); handled {
			return v, rk, mobile, ok
		}
	}
	st, simple := t.TypeArgs[0].(*ast.SimpleType)
	if !simple {
		return no()
	}
	owner := &ast.TypeIdent{Name: st.Name, Line: st.Line, Col: st.Col}
	call := *t
	call.TypeArgs = nil
	call.Func = &ast.FieldAccess{Object: owner, Field: fa.Field, Line: fa.Line, Col: fa.Col}
	return bl.lower(&call)
}

// returnSelfDispatch lowers `Iface.method(args)` whose method declares self
// only in its RETURN and whose call names no type argument:
// `notes: List<Note> = try FromJson.from_json(json)`. No argument carries the
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
	// The interface spelling only: stdKindQualCall re-lowers the call with
	// the solved type as its owner at the same position, and that call must
	// not come back here.
	owner := bl.g.fa.References[analysis.Pos{Line: ti.Line, Col: ti.Col}]
	if owner == nil || owner.Kind != analysis.SymbolInterface || owner.Name != ti.Name {
		return no()
	}
	ref := bl.g.fa.References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
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
	return bl.stdKindQualCall(t, k, fa.Field.Name)
}
