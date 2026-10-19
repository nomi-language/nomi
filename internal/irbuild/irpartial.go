package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// partialApplication lowers the partial capture `&add(1, &1)` (capture,
// whose body is t): a function of its open arguments, in the order they are
// written, that calls the callee with every slot filled. Nothing is
// evaluated when the function is made: a call of it evaluates the written
// arguments and calls the callee, as the equivalent `fn y -> add(1, y)`
// does.
//
// The callee is anything a call can name: a same-file function or a local
// function value whose lambda the builder can see, or, through the
// signature the checker instantiated at the call (partialCalleeByType), a
// generic function, an owner- or interface-qualified function, or another
// file's function. Arguments reach slots as a full call's do (argSlotPlan);
// a slot given twice declines.
//
// Each open argument becomes a parameter named so no Nomi program can spell
// it, so no parameter shadows a name the callee expression starts from
// (`&maybe.Maybe.map(&1, f)`, whose first parameter is `maybe`). It carries
// the callee's default when the builder can see the declaration, so an
// argument the call of the partial omits takes it, and partialOpen hands the
// lambda the slot's kind and the callee's parameter name, by which the
// partial is called (`f(port: 3000)`). A slot the partial does not mention
// takes the callee's default in the body's call, which leaves the slot out.
// An open slot's default is admitted only when it is a literal, which reads
// the same wherever it is lowered. The body's call names the callee exactly
// as the partial wrote it and holds the written arguments themselves, so it
// is lowered as the same call written in full would be. A parameter past the
// open arguments, which the function type the checker gave the capture has
// when its position expects more (`Iter.zip_with(xs, ys, &f(&1))`), is
// taken and ignored.
func (bl *irScalarBuilder) partialApplication(capture *ast.Capture, t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) {
		irDeclineNote("a partial application over a callee whose parameters the builder cannot see, or with a non-literal default")
		return ir.NoTemp, kindInvalid, false, false
	}
	params, kinds, sig, ok := bl.partialCallee(t)
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
	// (`&connect(timeout: &1, host: "h", &2)` leaves `port` open).
	plan, planned := argSlotPlan(t.Args, kinds, names)
	if !planned {
		return no()
	}
	written := make([]ast.Node, len(params))
	filled := make([]bool, len(params))
	placeholder := make([]ast.Node, len(params))
	var open []int
	for i, a := range t.Args {
		slot, value := plan.slots[i], argExpr(a)
		if slot < 0 || slot >= len(params) || filled[slot] {
			return no()
		}
		filled[slot] = true
		if _, isOpen := value.(*ast.Placeholder); isOpen {
			open = append(open, slot)
			placeholder[slot] = value
			continue
		}
		written[slot] = value
	}
	if len(open) == 0 {
		return no()
	}
	lam := &ast.Lambda{Line: t.Line, Col: t.Col, EndLine: t.Line, EndCol: t.Col}
	calleeOrder := bl.partialCalleeOrder(t, len(params))
	slots := partialSlots{kinds: make([]kind, 0, len(open)), names: make([]string, 0, len(open)),
		order: partialFillOrder(open, calleeOrder)}
	openName := make([]string, len(params))
	for _, slot := range open {
		p := params[slot]
		dflt := p.Default
		if dflt != nil && !irClosedLiteral(dflt) {
			fill, ok := bl.partialCalleeDefault(t, params, sig, slot, kinds, written, filled, openName, calleeOrder)
			if !ok {
				return no()
			}
			if slots.defaults == nil {
				slots.defaults = map[int]func(*irScalarBuilder) (ir.Temp, bool){}
			}
			slots.defaults[len(lam.Params)] = fill
			// The supplier answers fill's value; the placeholder stands
			// for the default only so the parameter has one.
			dflt = placeholder[slot]
		}
		openName[slot] = "%open" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
		lam.Params = append(lam.Params, ast.Param{Name: openName[slot], Default: dflt, Line: p.Line, Col: p.Col})
		slots.kinds = append(slots.kinds, kinds[slot])
		name := p.Name
		if p.Destructure != nil || name == "_" {
			// A pattern or a bare `_` is no name a call can write.
			name = ""
		}
		slots.names = append(slots.names, name)
	}
	if ft, _ := analysis.ResolveTypeVar(bl.g.srcFA().ExprTypes[capture]).(*analysis.FuncType); ft != nil && len(ft.Params) > len(open) {
		// The parameters the position expects past the open arguments,
		// which the function ignores.
		for i := len(open); i < len(ft.Params); i++ {
			k := bl.g.project(ft.Params[i])
			if !irCallableValueKind(k) && k != kindUnit {
				return no()
			}
			lam.Params = append(lam.Params, ast.Param{Name: captureParamName(capture, i+1), Line: t.Line, Col: t.Col})
			slots.kinds = append(slots.kinds, k)
			slots.names = append(slots.names, "")
			if slots.order != nil {
				slots.order = append(slots.order, i)
			}
		}
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
	// The written arguments are evaluated in the order the partial's call
	// evaluates them (argSlotPlan's order: positionals, then named
	// arguments, each in written order), as the equivalent lambda's call
	// would. When that is not the order of their slots, the body's call
	// names every argument, in that order, so it evaluates them so; a
	// callee with a parameter no call can name takes them in slot order.
	inOrder, nameable := true, true
	prevSlot := -1
	for _, i := range plan.order {
		slot := plan.slots[i]
		if written[slot] != nil {
			inOrder = inOrder && slot > prevSlot
			prevSlot = slot
		}
		nameable = nameable && names[slot] != "" && names[slot] != "_"
	}
	if !inOrder && nameable {
		var args []ast.Node
		for _, i := range plan.order {
			slot := plan.slots[i]
			arg := written[slot]
			if arg == nil {
				arg = &ast.Ident{Name: openName[slot], Line: t.Line, Col: t.Col}
			}
			args = append(args, &ast.NamedArg{Name: names[slot], Value: arg, Line: t.Line, Col: t.Col})
		}
		return bl.partialLambda(lam, t, args, slots)
	}
	var args []ast.Node
	for slot, p := range params[:last+1] {
		line, col := t.Line, t.Col
		var arg ast.Node
		switch {
		case written[slot] != nil:
			arg = written[slot]
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
	return bl.partialLambda(lam, t, args, slots)
}

// partialLambda lowers lam, whose body is the call t with args, as the
// function a partial capture stands for, its parameters taking slots.
func (bl *irScalarBuilder) partialLambda(lam *ast.Lambda, t *ast.Call, args []ast.Node, slots partialSlots) (ir.Temp, kind, bool, bool) {
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
	// defaults answers, by the lambda's parameter index, the default of
	// an open slot whose callee default is not a literal: lowered into
	// the slot's supplier (the builder it is given), in the callee's
	// scope, from the arguments before the slot (partialCalleeDefault).
	defaults map[int]func(*irScalarBuilder) (ir.Temp, bool)
	// order is the lambda's parameters in the order a call fills the ones
	// it omits (irLambdaGroup.order): its placeholders in the order the
	// callee fills their slots, nil when that is the written order.
	order []int
}

// partialCalleeOrder is the order a call of the partial's callee fills the
// slots it omits: a function value's group order, or the slots' own.
func (bl *irScalarBuilder) partialCalleeOrder(t *ast.Call, n int) []int {
	if callee, direct := t.Func.(*ast.Ident); direct && bl.isLocalCallable(callee.Name) {
		if v, _, _, ok := bl.lower(callee); ok {
			if plan := bl.callablePlan(v); plan != nil && plan.group != nil && len(plan.group.params) == n {
				return plan.group.fillOrder()
			}
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	return order
}

// partialFillOrder is the partial's parameters, its placeholders in written
// order (open holds each one's callee slot), in the order the callee fills
// their slots; nil when that is the written order.
func partialFillOrder(open, calleeOrder []int) []int {
	var order []int
	for _, slot := range calleeOrder {
		for j, s := range open {
			if s == slot {
				order = append(order, j)
			}
		}
	}
	for j, p := range order {
		if p != j {
			return order
		}
	}
	return nil
}

// slotsBefore is the slots before slot in order.
func slotsBefore(order []int, slot int) []int {
	for at, s := range order {
		if s == slot {
			return order[:at]
		}
	}
	return nil
}

// partialCalleeDefault is how a partial's supplier for the open slot slot
// evaluates the callee's default there, `port` in `&conn("h", &1)` over
// `fn conn(host: String, port: Int = base() + 1)`: as a call omitting the
// slot does (callDefaults), in the callee's scope and on each call of the
// partial that omits it, with the callee's earlier parameters bound to the
// written arguments (which the supplier captures) and the open slots
// before it (the supplier's own parameters). The callee is a function of
// this file (sig, or the generic instance partialMonoSig finds), or a local
// function value whose default suppliers the builder holds: the supplier
// captures the value's suppliers as any closure calling it does
// (capturedPlan) and calls the slot's one, as a call omitting the slot
// does (indirectCall). An earlier slot the partial leaves unmentioned must
// have a literal default, since the call in the partial's body evaluates
// that default again.
func (bl *irScalarBuilder) partialCalleeDefault(t *ast.Call, params []ast.Param, sig *fnSig, slot int, kinds []kind, written []ast.Node, filled []bool, openName []string, calleeOrder []int) (func(*irScalarBuilder) (ir.Temp, bool), bool) {
	callee, direct := t.Func.(*ast.Ident)
	value := direct && bl.isLocalCallable(callee.Name)
	if sig == nil && !value {
		sig = bl.partialMonoSig(t)
	}
	if fd, isFn := bl.g.calleeDecl(t.Func).(*ast.FuncDef); sig == nil && !value && isFn && bl.g.declaredHere(fd) {
		// An owner-qualified function of this file: its declaration at the
		// kinds the checker instantiated the call at, as a call naming its
		// arguments fills an omitted slot (namedQualCall). Another file's
		// default is lowered in that file, through its accessor
		// (siblingdefault.go), which this does not reach.
		sig = &fnSig{decl: fd, params: kinds}
	}
	var sibling func(child *irScalarBuilder, args []ir.Temp) bool
	if sig == nil && !value {
		sibling = bl.partialSiblingDefaults(t, kinds, slot)
	}
	if (sig == nil && !value && sibling == nil) || (sig != nil && len(sig.decl.Params) != len(kinds)) {
		irDeclineNote("a partial's open slot whose default is not a literal, over a callee outside this file's functions")
		return nil, false
	}
	// The slots the callee fills before this one. A placeholder among them
	// may be written after this one (`&span(b: &1, &2)`): the partial fills
	// its parameters in the callee's order (partialFillOrder), so this
	// slot's supplier takes it.
	before := slotsBefore(calleeOrder, slot)
	for _, s := range before {
		if p := params[s]; !filled[s] && p.Default != nil && !irClosedLiteral(p.Default) {
			irDeclineNote("a partial's open slot whose default is not a literal, after an unmentioned slot whose default is not either")
			return nil, false
		}
	}
	fill := func(child *irScalarBuilder) (ir.Temp, bool) {
		args := make([]ir.Temp, len(kinds))
		for s := range args {
			args[s] = ir.NoTemp
		}
		for _, s := range before {
			var v ir.Temp
			var k kind
			var ok bool
			switch {
			case written[s] != nil:
				// A written argument the default reads, evaluated for it
				// on each call that omits the slot.
				v, k, _, ok = child.lowerTypedOperand(written[s], kinds[s])
				if ok && k != kinds[s] {
					v, k, ok = child.coerceEmpty(written[s], v, k, kinds[s])
				}
			case filled[s]:
				v, k, _, ok = child.lower(&ast.Ident{Name: openName[s], Line: t.Line, Col: t.Col})
			default:
				continue
			}
			if !ok || k != kinds[s] {
				return ir.NoTemp, false
			}
			args[s] = v
		}
		if sig != nil {
			if !child.callDefaultsThrough(sig, args, slot+1) {
				return ir.NoTemp, false
			}
			return args[slot], true
		}
		if sibling != nil {
			if !sibling(child, args[:slot+1]) {
				return ir.NoTemp, false
			}
			return args[slot], true
		}
		fn, _, _, ok := child.lower(callee)
		plan := child.callablePlan(fn)
		if !ok || plan == nil || plan.group == nil || len(plan.group.suppliers) != len(args) {
			return ir.NoTemp, false
		}
		for _, s := range append(before[:len(before):len(before)], slot) {
			if args[s] != ir.NoTemp {
				continue
			}
			supplier := plan.group.suppliers[s]
			if supplier == ir.NoTemp {
				return ir.NoTemp, false
			}
			call := ir.NewIndirectCall(child.g.irNodePos(t), child.f.NewTemp(), irCallSite(t), supplier, argsAt(args, plan.group.before(s))...)
			child.b.Append(call)
			child.side(call.Dst(), irScalarSide{k: kinds[s], deferrable: true})
			args[s] = call.Dst()
		}
		return args[slot], true
	}
	return fill, true
}

// partialCallee is the parameter declarations and kinds of a partial
// application's callee, and its signature when it is a module function of
// this file, whose defaults callDefaults evaluates in its own scope.
func (bl *irScalarBuilder) partialCallee(t *ast.Call) ([]ast.Param, []kind, *fnSig, bool) {
	callee, direct := t.Func.(*ast.Ident)
	if direct && bl.isLocalCallable(callee.Name) {
		v, fk, _, ok := bl.lower(callee)
		if !ok || fk.tag != tagFunc {
			return nil, nil, nil, false
		}
		plan := bl.callablePlan(v)
		if plan == nil || plan.source == nil || len(plan.names) != len(plan.source.Params) {
			return nil, nil, nil, false
		}
		// The names a call of the value writes are the plan's: a partial's
		// lambda names its own parameters so no program can spell them.
		params := make([]ast.Param, len(plan.source.Params))
		for i, p := range plan.source.Params {
			params[i] = ast.Param{Name: plan.names[i], Default: p.Default, Line: p.Line, Col: p.Col}
		}
		return params, funcParams(fk), nil, true
	}
	if direct {
		if _, shadowed := bl.g.lookup(callee.Name); !shadowed && bl.g.stdlibSibling(callee.Name) == nil {
			if sig := bl.g.funcs[callee.Name]; sig != nil && sig.lowerable && sig.decl != nil && sig.dict == nil && sig.tps == nil {
				return sig.decl.Params, sig.params, sig, true
			}
		}
	}
	params, kinds, ok := bl.partialCalleeByType(t)
	return params, kinds, nil, ok
}

// partialSiblingDefaults is how a partial over another file's function
// (`&lib.conn("h", &1)`, or `conn` after `import lib.{conn}`) fills the
// slots up to slot it omits: with the accessors the declaring file builds
// for its defaults, as a call omitting them does (siblingFill), over a
// declaration cut after slot so no later default runs. nil when the
// callee is no sibling file's function the builder can call.
func (bl *irScalarBuilder) partialSiblingDefaults(t *ast.Call, kinds []kind, slot int) func(*irScalarBuilder, []ir.Temp) bool {
	if bl.g.files == nil {
		return nil
	}
	var site fileSite
	found := false
	switch callee := t.Func.(type) {
	case *ast.Ident:
		s, ok := bl.g.files.lookupBare(bl.g.srcFA(), callee)
		site, found = s, ok && s.fn != nil && s.unit != bl.g.fileUnit
	case *ast.FieldAccess:
		site, found = bl.siblingQualSite(callee)
	}
	if !found || site.host || site.fn.decl == nil || len(site.fn.decl.Params) != len(kinds) {
		return nil
	}
	f := site.fn
	var result kind
	var sym *ir.Symbol
	ok := false
	if f.generic {
		var params []kind
		params, result, sym, ok = bl.g.siblingGenericInstance(t, site.unit, f)
		ok = ok && len(params) == len(kinds)
	} else if f.lowerable() {
		_, result, ok = bl.g.siblingSignature(f)
	}
	if !ok {
		return nil
	}
	plan := bl.g.siblingFuncPlan(site.unit, f, result)
	if sym != nil {
		plan.sym = sym
	}
	cut := *f.decl
	cut.Params = f.decl.Params[:slot+1]
	owner := bl.g.unitGen(site.unit)
	return func(child *irScalarBuilder, args []ir.Temp) bool {
		return child.siblingFill(t, owner, plan.sym, &cut, kinds[:slot+1], args, nil)
	}
}

// partialMonoSig is the signature of the instance a partial over a generic
// function of this file names, at the type arguments the checker solved
// for the call, as a direct call to it finds it; nil otherwise.
func (bl *irScalarBuilder) partialMonoSig(t *ast.Call) *fnSig {
	callee, direct := t.Func.(*ast.Ident)
	if !direct {
		return nil
	}
	if _, shadowed := bl.g.lookup(callee.Name); shadowed || bl.g.stdlibSibling(callee.Name) != nil {
		return nil
	}
	tpl := bl.g.irMonoTemplate(bl.g.funcs[callee.Name])
	if tpl == nil {
		return nil
	}
	typeArgs, ok := bl.g.checkedMonoTypeArgs(t, tpl)
	if !ok {
		typeArgs, ok = bl.g.checkedMonoTypeArgsFilled(t, tpl)
	}
	if !ok {
		return nil
	}
	inst, why, _ := bl.g.resolveMonoInstance(tpl, typeArgs, callee.Name)
	if why != "" || inst == nil || inst.sig == nil || !inst.sig.lowerable || inst.sig.decl == nil {
		return nil
	}
	return inst.sig
}

// partialCalleeByType reads the callee's parameters off the signature the
// checker instantiated at the call: `&Console.write_line(c, &1)` with c a
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
	switch d := g.calleeDecl(callee).(type) {
	case *ast.FuncDef:
		return d.Params
	case *ast.InterfaceMethod:
		return d.Params
	case *ast.ExternFunc:
		return d.Params
	}
	return nil
}

// declaredHere reports whether fd is one of this file's functions: a
// top-level one or an impl block's.
func (g *gen) declaredHere(fd *ast.FuncDef) bool {
	for _, n := range g.nodes {
		if n == ast.Node(fd) {
			return true
		}
		if ib, isImpl := n.(*ast.ImplBlock); isImpl {
			for _, item := range ib.Items {
				if item == ast.Node(fd) {
					return true
				}
			}
		}
	}
	return false
}

// calleeDecl is the declaration a callee expression resolves to, or nil.
func (g *gen) calleeDecl(callee ast.Node) ast.Node {
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
	return sym.Node
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

// hasPlaceholder reports a call with a `&1` argument, positional or named
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
