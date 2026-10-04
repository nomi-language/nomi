package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// callType is the result type of a call the checker has not typed (its
// statement does not parse yet). A generic callee's type parameters are
// solved from the arguments' types by the checker's own unification
// (analysis.FileAnalysis.CallResultType), so `first(points)` is a Point.
// piped, when set, is the pipe whose stage the call is: the piped value
// fills the first parameter. A call written with explicit type arguments
// (`f<Int>(x)`) is not solved here.
func (r *completionRequest) callType(call *ast.Call, piped *ast.Binary) analysis.Type {
	ft, ok := analysis.ResolveTypeVar(r.exprType(call.Func)).(*analysis.FuncType)
	if !ok {
		return nil
	}
	if !analysis.ContainsTypeParam(ft.Return) {
		return ft.Return
	}
	if call.TypeArgs != nil {
		return nil
	}
	shape := analysis.CallShape{
		Args:       call.Args,
		ParamNames: paramNamesOf(r.calleeSymbol(call.Func)),
		ArgType:    r.exprType,
	}
	if piped != nil {
		shape.Piped = true
		shape.PipedType = r.exprType(piped.Left)
	}
	return r.fa.CallResultType(ft, shape)
}

// pipeType is the type of a pipe `left |> stage` the checker has not
// typed: a stage call (`|> Iter.to_list()`) or a bare function
// (`|> Iter.to_list`) called with the piped value. A keyword stage
// (`try`, `dbg`, `case`) is not read.
func (r *completionRequest) pipeType(p *ast.Binary) analysis.Type {
	switch stage := p.Right.(type) {
	case *ast.Call:
		return r.callType(stage, p)
	case *ast.Ident, *ast.FieldAccess:
		return r.callType(&ast.Call{Func: stage}, p)
	}
	return nil
}

// paramNamesOf lists a function's declared parameter names, for routing
// named arguments; nil when fn is no declared function.
func paramNamesOf(fn *analysis.Symbol) []string {
	fn = realSymbol(fn)
	if fn == nil {
		return nil
	}
	var params []ast.Param
	switch n := fn.Node.(type) {
	case *ast.FuncDef:
		params = n.Params
	case *ast.ExternFunc:
		params = n.Params
	case *ast.InterfaceMethod:
		params = n.Params
	}
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}
