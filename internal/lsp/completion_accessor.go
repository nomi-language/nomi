package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// accessorCandidates offers the fields a field accessor being typed can
// read: `Iter.map(users, .‸)` expects `(User) -> U`, so User's fields, and
// `.address.‸` the fields of User's address. ok is false when the position
// does not expect a one-parameter function over a type with fields, and the
// caller offers variants instead (a leading dot also starts `.Variant`).
func (r *completionRequest) accessorCandidates() ([]candidate, bool) {
	acc := r.ctx.accessor
	if acc == nil {
		return nil, false
	}
	ft, ok := analysis.ResolveTypeVar(r.expected).(*analysis.FuncType)
	if !ok || len(ft.Params) != 1 {
		return nil, false
	}
	t := ft.Params[0]
	for _, seg := range acc.Path[:min(r.ctx.accessorSeg, len(acc.Path))] {
		f, found := fieldOf(t, seg.Name)
		if !found {
			return nil, true
		}
		t = f.Type
	}
	fields := fieldsOf(t)
	if len(fields) == 0 {
		return nil, false
	}
	return fieldCandidates(fields, r.declOf(t)), true
}

// accessorParamType is the parameter type a field accessor at argument idx
// of call fills, with the callee's type parameters solved from the call's
// other arguments, and from the piped value when the call is a pipe stage
// (callHop is the call's hop on the path). The checker solves the same
// parameters before it reaches the accessor; this answers for a statement
// it has not seen.
func (r *completionRequest) accessorParamType(h *sentinelHit, callHop int, call *ast.Call, idx int) analysis.Type {
	ft, ok := analysis.ResolveTypeVar(r.exprType(call.Func)).(*analysis.FuncType)
	if !ok || !analysis.ContainsTypeParam(ft) || call.TypeArgs != nil {
		return nil
	}
	shape := analysis.CallShape{
		Args:       call.Args,
		ParamNames: paramNamesOf(r.calleeSymbol(call.Func)),
		ArgType:    r.exprType,
	}
	_, field := h.at(callHop)
	parent, _ := h.at(callHop + 1)
	if b, isPipe := parent.(*ast.Binary); isPipe && b.Op == "|>" && field == "Right" {
		shape.Piped = true
		shape.PipedType = r.exprType(b.Left)
	}
	return r.fa.CallArgParamType(ft, shape, idx)
}
