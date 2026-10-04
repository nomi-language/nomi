package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// namedArgCandidates offers, where a call's argument starts (`f(‸`,
// `f(a, ‸`, `xs |> Task.spawn_all(f, ‸`), the callee's parameters that no
// argument fills yet, each as `name: ` with a tab stop for the value. They
// join the expression candidates there, since a positional argument is an
// expression.
//
// A parameter with a default ranks above everything: skipping to it by name
// is what named arguments are for. A required parameter is offered too, at
// the rank of an imported name, because the language lets any parameter be
// named and naming a bare literal reads better (`max_running: 2`).
//
// Positional arguments before the cursor fill the slots from the first, a
// pipe's value fills the first, a lone positional after named arguments
// fills the first slot they leave empty, and a named argument anywhere in
// the call fills its own. A parameter named `self`, typed `self` (whose name
// each implementation picks) or destructured has no name to call it by.
func (r *completionRequest) namedArgCandidates() []candidate {
	h := r.ctx.hit
	if h == nil || h.field != "Name" {
		return nil
	}
	if _, ok := firstOf(h.at(0)).(*ast.Ident); !ok {
		return nil
	}
	call, ok := firstOf(h.at(1)).(*ast.Call)
	if _, field := h.at(0); !ok || field != "Args" {
		return nil
	}
	params, types := r.calleeParams(call.Func)
	if len(params) == 0 {
		return nil
	}
	filled := make([]bool, len(params))
	next := 0
	if _, field := h.at(1); field == "Right" {
		if b, ok := firstOf(h.at(2)).(*ast.Binary); ok && b.Op == "|>" {
			filled[0] = true
			next = 1
		}
	}
	cursorArg := h.path[len(h.path)-1].index
	afterNamed := false
	for i, a := range call.Args {
		if i == cursorArg {
			continue
		}
		if na, ok := a.(*ast.NamedArg); ok {
			for j, p := range params {
				if p.Name == na.Name {
					filled[j] = true
				}
			}
			afterNamed = true
			continue
		}
		if i > cursorArg {
			continue
		}
		if afterNamed {
			for j := range filled {
				if !filled[j] {
					filled[j] = true
					break
				}
			}
			continue
		}
		if next < len(filled) {
			filled[next] = true
		}
		next++
	}
	var out []candidate
	for i, p := range params {
		if filled[i] || p.Name == "" || p.Destructure != nil || p.Name == "self" || selfTyped(p) {
			continue
		}
		detail := ""
		switch {
		case p.TypeAnnotation != nil:
			detail = p.TypeAnnotation.TypeString()
		case i < len(types):
			detail = typeDisplay(types[i])
		}
		c := candidate{
			label:    p.Name + ":",
			kind:     protocol.CompletionItemKindProperty,
			filter:   p.Name,
			insert:   p.Name + ": ",
			snippet:  snippetEscape(p.Name) + ": ${1}",
			locality: 2,
			order:    i,
			noCall:   true,
		}
		if p.Default != nil {
			c.first = true
			detail += " (optional)"
		}
		c.detail = "named argument " + p.Name + ": " + detail
		out = append(out, c)
	}
	return out
}

// selfTyped reports a parameter typed exactly `self`, whose name each
// implementation chooses freely, so a call cannot rely on it.
func selfTyped(p ast.Param) bool {
	return p.TypeAnnotation != nil && p.TypeAnnotation.TypeString() == "self"
}

// calleeParams is the declared parameter list of the function a call's
// callee names, with its parameter types; nil for a callee that is not a
// declared function (a lambda value, a variant, a struct).
func (r *completionRequest) calleeParams(fn ast.Node) ([]ast.Param, []analysis.Type) {
	sym := r.calleeSymbol(fn)
	if sym == nil {
		return nil, nil
	}
	var types []analysis.Type
	if ft, ok := analysis.ResolveTypeVar(sym.Type).(*analysis.FuncType); ok {
		types = ft.Params
	}
	switch n := sym.Node.(type) {
	case *ast.FuncDef:
		return n.Params, types
	case *ast.ExternFunc:
		return n.Params, types
	case *ast.InterfaceMethod:
		return n.Params, types
	}
	return nil, nil
}
