package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// partialApplication lowers `add(1, _)`: the written arguments, positional and named, are evaluated now,
// in source order, and the result is a function of the placeholders, in the
// order they are written, that calls the callee with every slot filled.
//
// The callee is anything a call can name: a same-file function or a local
// function value whose lambda the builder can see, or, through the
// signature the checker instantiated at the call (partialCalleeByType), a
// generic function, an owner- or interface-qualified function, or another
// file's function. A positional argument means the slot it sits in; a named
// one names its slot; a slot given twice declines.
//
// The written arguments are bound to names no Nomi program can spell, so
// the lambda that stands for the partial captures them as it captures any
// local. Each placeholder becomes a parameter that IS the callee's own
// parameter declaration (name, default) when the builder can see it, so the
// partial is called by the callee's names and a placeholder the call omits
// takes the callee's default; its kind is the slot's, which partialKinds
// hands the lambda. A slot the partial does not mention takes the callee's
// default in the body's call. A default is admitted only when it is a
// literal, which reads the same wherever it is lowered. The body's call
// names the callee exactly as the partial wrote it, so it is lowered as
// the same call written in full would be.
func (bl *irScalarBuilder) partialApplication(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) {
		irDeclineNote("a partial application over a callee whose parameters the builder cannot see, or with a non-literal default")
		return ir.NoTemp, kindInvalid, false, false
	}
	params, kinds, root, ok := bl.partialCallee(t)
	if !ok || len(params) != len(kinds) {
		return no()
	}
	slotOf := func(name string) int {
		for i, p := range params {
			if p.Destructure == nil && p.Name == name {
				return i
			}
		}
		return -1
	}
	written := make([]string, len(params))
	filled := make([]bool, len(params))
	var open []int
	posIdx := 0
	for i, a := range t.Args {
		slot := posIdx
		value := a
		if na, named := a.(*ast.NamedArg); named {
			slot, value = slotOf(na.Name), na.Value
		} else {
			posIdx++
		}
		if slot < 0 || slot >= len(params) || filled[slot] {
			return no()
		}
		filled[slot] = true
		if _, isOpen := value.(*ast.Placeholder); isOpen {
			open = append(open, slot)
			continue
		}
		v, k, _, ok := bl.lowerTypedOperand(value, kinds[slot])
		if ok && k != kinds[slot] {
			v, k, ok = bl.coerceEmpty(value, v, k, kinds[slot])
		}
		if !ok || k != kinds[slot] {
			return ir.NoTemp, kindInvalid, false, false
		}
		name := "%partial" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(i)
		bl.patternBinding(value, name, v, k)
		written[slot] = name
	}
	if len(open) == 0 {
		return no()
	}
	lam := &ast.Lambda{Line: t.Line, Col: t.Col, EndLine: t.Line, EndCol: t.Col}
	openKinds := make([]kind, 0, len(open))
	for _, slot := range open {
		p := params[slot]
		if p.Destructure != nil || ast.IsDiscardName(p.Name) || p.Name == root ||
			(p.Default != nil && !irClosedLiteral(p.Default)) {
			return no()
		}
		lam.Params = append(lam.Params, p)
		openKinds = append(openKinds, kinds[slot])
	}
	args := make([]ast.Node, len(params))
	for slot, p := range params {
		line, col := t.Line, t.Col
		switch {
		case written[slot] != "":
			args[slot] = &ast.Ident{Name: written[slot], Line: line, Col: col}
		case filled[slot]:
			args[slot] = &ast.Ident{Name: p.Name, Line: line, Col: col}
		case p.Default != nil && irClosedLiteral(p.Default):
			args[slot] = p.Default
		default:
			return no()
		}
	}
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{
		&ast.Call{Func: t.Func, Args: args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col},
	}}
	if bl.partialKinds == nil {
		bl.partialKinds = map[*ast.Lambda][]kind{}
	}
	bl.partialKinds[lam] = openKinds
	defer delete(bl.partialKinds, lam)
	return bl.lambda(lam)
}

// partialCallee is the parameter declarations and kinds of a partial
// application's callee, and the name its callee expression starts from (a
// parameter of that name would shadow it inside the partial's body).
func (bl *irScalarBuilder) partialCallee(t *ast.Call) ([]ast.Param, []kind, string, bool) {
	callee, direct := t.Func.(*ast.Ident)
	if direct && bl.isLocalCallable(callee.Name) {
		v, fk, _, ok := bl.lower(callee)
		if !ok || fk.tag != tagFunc {
			return nil, nil, "", false
		}
		if plan := bl.callablePlan(v); plan != nil && plan.source != nil {
			return plan.source.Params, funcParams(fk), callee.Name, true
		}
		return nil, nil, "", false
	}
	if direct {
		if _, shadowed := bl.g.lookup(callee.Name); !shadowed && bl.g.stdlibSibling(callee.Name) == nil {
			if sig := bl.g.funcs[callee.Name]; sig != nil && sig.lowerable && sig.decl != nil && sig.dict == nil && sig.tps == nil {
				return sig.decl.Params, sig.params, callee.Name, true
			}
		}
	}
	return bl.partialCalleeByType(t)
}

// partialCalleeByType reads the callee's parameters off the signature the
// checker instantiated at the call: `Console.write_line(c, _)` with c a
// Stdout is `(Stdout, String) -> Unit`. Their declarations come from the
// symbol the callee resolves to; a callee with none in view gets parameters
// named so no program can spell them, which only positional arguments reach.
func (bl *irScalarBuilder) partialCalleeByType(t *ast.Call) ([]ast.Param, []kind, string, bool) {
	kinds, _, ok := bl.g.checkedValueKinds(t.Func)
	if !ok {
		return nil, nil, "", false
	}
	root := ""
	switch f := t.Func.(type) {
	case *ast.Ident:
		root = f.Name
	case *ast.FieldAccess:
		if obj, isIdent := f.Object.(*ast.Ident); isIdent {
			root = obj.Name
		}
	}
	params := bl.g.calleeDeclParams(t.Func)
	if len(params) != len(kinds) {
		params = make([]ast.Param, len(kinds))
		for i := range params {
			params[i] = ast.Param{Name: "%open" + strconv.Itoa(i), Line: t.Line, Col: t.Col}
		}
	}
	return params, kinds, root, true
}

// calleeDeclParams is the parameter list of the declaration a callee
// expression resolves to, or nil.
func (g *gen) calleeDeclParams(callee ast.Node) []ast.Param {
	if g.fa == nil {
		return nil
	}
	line, col := calleeRefPos(callee)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch d := sym.Node.(type) {
	case *ast.FuncDef:
		return d.Params
	case *ast.InterfaceMethod:
		return d.Params
	case *ast.ExternFunc:
		return d.Params
	}
	return nil
}

// isLocalCallable reports whether name is a function value this body or an
// enclosing one binds, which a call reaches through the value.
func (bl *irScalarBuilder) isLocalCallable(name string) bool {
	for scope := bl; scope != nil; scope = scope.parent {
		if k, bound := scope.boundK[name]; bound {
			return k.tag == tagFunc
		}
	}
	return false
}

// irClosedLiteral is a default that names nothing: a scalar literal, or its
// negation, or a Bool literal (lowered by name, see the TypeIdent arm of
// lower), which reads the same in any scope it is lowered in.
func irClosedLiteral(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit, *ast.StringLit:
		return true
	case *ast.TypeIdent:
		return x.Name == "True" || x.Name == "False"
	case *ast.Unary:
		if x.Op != "-" {
			return false
		}
		switch x.Right.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
			return true
		}
	}
	return false
}

// hasPlaceholder reports a call with a `_` argument, positional or named
// (`port: _`).
func hasPlaceholder(args []ast.Node) bool {
	for _, a := range args {
		if na, named := a.(*ast.NamedArg); named {
			a = na.Value
		}
		if _, open := a.(*ast.Placeholder); open {
			return true
		}
	}
	return false
}
