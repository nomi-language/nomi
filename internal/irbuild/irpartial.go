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
// file's function. Arguments reach slots as a full call's do (argSlotPlan);
// a slot given twice declines.
//
// The written arguments are bound to names no Nomi program can spell, so
// the lambda that stands for the partial captures them as it captures any
// local. Each placeholder becomes a parameter named the same way, so no
// parameter shadows a name the callee expression starts from
// (`maybe.Maybe.map(_, f)`, whose first parameter is `maybe`). It carries
// the callee's default when the builder can see the declaration, so a
// placeholder the call omits takes it, and partialOpen hands the lambda the
// slot's kind and the callee's parameter name, by which the partial is
// called (`f(port: 3000)`). A slot the partial does not mention takes the
// callee's default in the body's call, which leaves the slot out. An open
// slot's default is admitted only when it is a literal, which reads the same
// wherever it is lowered. The body's call
// names the callee exactly as the partial wrote it, so it is lowered as the
// same call written in full would be.
func (bl *irScalarBuilder) partialApplication(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) {
		irDeclineNote("a partial application over a callee whose parameters the builder cannot see, or with a non-literal default")
		return ir.NoTemp, kindInvalid, false, false
	}
	params, kinds, ok := bl.partialCallee(t)
	if !ok || len(params) != len(kinds) {
		return no()
	}
	names := make([]string, len(params))
	for i, p := range params {
		if p.Destructure == nil {
			names[i] = p.Name
		}
	}
	// The arguments reach their slots as a full call's do, so a positional
	// written after a named argument skips the slot the name claimed
	// (`connect(timeout: _, host: "h", _)` leaves `port` open).
	plan, planned := argSlotPlan(t.Args, kinds, names)
	if !planned {
		return no()
	}
	written := make([]string, len(params))
	filled := make([]bool, len(params))
	var open []int
	for i, a := range t.Args {
		slot, value := plan.slots[i], argExpr(a)
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
	slots := partialSlots{kinds: make([]kind, 0, len(open)), names: make([]string, 0, len(open))}
	openName := make([]string, len(params))
	for _, slot := range open {
		p := params[slot]
		if p.Default != nil && !irClosedLiteral(p.Default) {
			return no()
		}
		openName[slot] = "%open" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
		lam.Params = append(lam.Params, ast.Param{Name: openName[slot], Default: p.Default, Line: p.Line, Col: p.Col})
		slots.kinds = append(slots.kinds, kinds[slot])
		name := p.Name
		if p.Destructure != nil || name == "_" {
			// A pattern or a bare `_` is no name a call can write.
			name = ""
		}
		slots.names = append(slots.names, name)
	}
	// A slot the partial does not mention is left out of the body's call,
	// which fills it from the callee's default in the callee's scope, as any
	// call omitting it does. A trailing one is simply not written; one
	// before a written slot is skipped by writing the call's arguments by
	// name, unless its default is a literal, which reads the same written
	// in place.
	last := -1
	named := false
	for slot := range params {
		if filled[slot] {
			last = slot
		}
	}
	for slot, p := range params[:last+1] {
		if !filled[slot] && (p.Default == nil || !irClosedLiteral(p.Default)) {
			named = true
		}
	}
	var args []ast.Node
	for slot, p := range params[:last+1] {
		line, col := t.Line, t.Col
		var arg ast.Node
		switch {
		case written[slot] != "":
			arg = &ast.Ident{Name: written[slot], Line: line, Col: col}
		case filled[slot]:
			arg = &ast.Ident{Name: openName[slot], Line: line, Col: col}
		case named && p.Default != nil:
			continue
		case p.Default != nil && irClosedLiteral(p.Default):
			arg = p.Default
		default:
			return no()
		}
		if named {
			if names[slot] == "" || names[slot] == "_" {
				return no()
			}
			arg = &ast.NamedArg{Name: names[slot], Value: arg, Line: line, Col: col}
		}
		args = append(args, arg)
	}
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{
		&ast.Call{Func: t.Func, Args: args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col},
	}}
	if bl.partialOpen == nil {
		bl.partialOpen = map[*ast.Lambda]partialSlots{}
	}
	bl.partialOpen[lam] = slots
	defer delete(bl.partialOpen, lam)
	return bl.lambda(lam)
}

// partialSlots is what a partial application's lambda takes at its open
// slots, in the order the placeholders are written: each one's kind, and
// the callee's name for it, which a call of the partial writes ("" for a
// slot no call can name).
type partialSlots struct {
	kinds []kind
	names []string
}

// partialCallee is the parameter declarations and kinds of a partial
// application's callee.
func (bl *irScalarBuilder) partialCallee(t *ast.Call) ([]ast.Param, []kind, bool) {
	callee, direct := t.Func.(*ast.Ident)
	if direct && bl.isLocalCallable(callee.Name) {
		v, fk, _, ok := bl.lower(callee)
		if !ok || fk.tag != tagFunc {
			return nil, nil, false
		}
		plan := bl.callablePlan(v)
		if plan == nil || plan.source == nil || len(plan.names) != len(plan.source.Params) {
			return nil, nil, false
		}
		// The names a call of the value writes are the plan's: a partial's
		// lambda names its own parameters so no program can spell them.
		params := make([]ast.Param, len(plan.source.Params))
		for i, p := range plan.source.Params {
			params[i] = ast.Param{Name: plan.names[i], Default: p.Default, Line: p.Line, Col: p.Col}
		}
		return params, funcParams(fk), true
	}
	if direct {
		if _, shadowed := bl.g.lookup(callee.Name); !shadowed && bl.g.stdlibSibling(callee.Name) == nil {
			if sig := bl.g.funcs[callee.Name]; sig != nil && sig.lowerable && sig.decl != nil && sig.dict == nil && sig.tps == nil {
				return sig.decl.Params, sig.params, true
			}
		}
	}
	return bl.partialCalleeByType(t)
}

// partialCalleeByType reads the callee's parameters off the signature the
// checker instantiated at the call: `Console.write_line(c, _)` with c a
// Stdout is `(Stdout, String) -> Unit`. Their declarations come from the
// symbol the callee resolves to; a callee with none in view gets unnamed
// parameters, which only positional arguments reach.
func (bl *irScalarBuilder) partialCalleeByType(t *ast.Call) ([]ast.Param, []kind, bool) {
	kinds, _, ok := bl.g.checkedFuncKinds(t.Func, true)
	if !ok {
		return nil, nil, false
	}
	params := bl.g.calleeDeclParams(t.Func)
	if len(params) != len(kinds) {
		params = make([]ast.Param, len(kinds))
		for i := range params {
			params[i] = ast.Param{Line: t.Line, Col: t.Col}
		}
	}
	return params, kinds, true
}

// calleeDeclParams is the parameter list of the declaration a callee
// expression resolves to, or nil.
func (g *gen) calleeDeclParams(callee ast.Node) []ast.Param {
	if g.fa == nil {
		return nil
	}
	line, col := calleeRefPos(callee)
	sym := g.srcFA().References[analysis.Pos{Line: line, Col: col}]
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
