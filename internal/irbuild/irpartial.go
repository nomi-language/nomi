package irbuild

import (
	"fmt"
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
// An open slot's computed default is the callee's, evaluated in the callee's
// scope by the slot's supplier (partialCalleeDefault). When the partial
// leaves a computed default unmentioned before it (`b` in `&span(&1, c:
// &2)` over `fn span(a, b = base() + 1, c = base() * 2)`), the partial also
// gets an entry (irLambdaPlan.entry) that takes those hidden slots after
// its own: a call that leaves an argument out goes through the entry and
// evaluates every default it leaves out once, in the callee's order, and a
// call that passes every argument goes through the partial, whose body's
// call evaluates the hidden defaults. Each route to the callee's defaults
// (this file's function, a local function value, another file's or std's)
// fills a hidden slot as it fills an open one. The body's call names the callee exactly
// as the partial wrote it and holds the written arguments themselves, so it
// is lowered as the same call written in full would be. A parameter past the
// open arguments, which the function type the checker gave the capture has
// when its position expects more (`Iter.zip_with(xs, ys, &f(&1))`), is
// taken and ignored.
func (bl *irScalarBuilder) partialApplication(capture *ast.Capture, t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) {
		bl.g.dc.note("a partial application over a callee whose parameters the builder cannot see, or with a non-literal default")
		return ir.NoTemp, kindInvalid, false, false
	}
	t = bl.partialOverEntry(t)
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
	calleeOrder := bl.partialCalleeOrder(t, len(params))
	// The slots the partial leaves unmentioned whose computed default an
	// open slot's computed default comes after (partialHiddenSlots). A
	// call of the partial that leaves an argument out goes through the
	// partial's entry, which takes these too, so each default is evaluated
	// once, at the call, in the callee's order.
	hidden := partialHiddenSlots(params, filled, open, calleeOrder)
	openName := make([]string, len(params))
	for _, slot := range open {
		openName[slot] = "%open" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
	}
	for _, slot := range hidden {
		openName[slot] = "%hidden" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
	}
	// The parameters the position expects past the open arguments, which
	// the function ignores.
	var extra []kind
	if ft, _ := analysis.ResolveClosed(bl.g.srcFA().ExprTypes[capture]).(*analysis.FuncType); ft != nil && len(ft.Params) > len(open) {
		for i := len(open); i < len(ft.Params); i++ {
			k := bl.g.project(ft.Params[i])
			if !irCallableValueKind(k) && k != kindUnit {
				return no()
			}
			extra = append(extra, k)
		}
	}
	pc := partialCall{t: t, capture: capture, params: params, kinds: kinds, sig: sig, names: names, plan: plan,
		written: written, filled: filled, placeholder: placeholder, openName: openName, calleeOrder: calleeOrder, open: open, extra: extra}
	if len(hidden) == 0 {
		lam, slots, ok := bl.partialLam(pc, nil, true)
		if !ok {
			return no()
		}
		args, ok := bl.partialBodyArgs(pc, filled)
		if !ok {
			return no()
		}
		return bl.partialLambda(capture, lam, t, args, slots)
	}
	// The entry: the open slots, the ignored ones and the hidden ones, with
	// every computed default's supplier.
	withHidden := append([]bool(nil), filled...)
	for _, slot := range hidden {
		withHidden[slot] = true
	}
	entryPC := pc
	entryPC.filled = withHidden
	entryLam, entrySlots, ok := bl.partialLam(entryPC, hidden, true)
	if !ok {
		return no()
	}
	entryArgs, ok := bl.partialBodyArgs(entryPC, withHidden)
	if !ok {
		return no()
	}
	entry, ek, _, ok := bl.partialEntry(entryLam, t, entryArgs, entrySlots)
	if !ok {
		return no()
	}
	entryPlan := bl.callablePlan(entry)
	if entryPlan == nil || entryPlan.group == nil {
		// lambda answers a FuncValue whose side holds its plan and group.
		panic("irbuild: a partial's entry with no lambda plan")
	}
	// The partial itself: a call that passes every argument evaluates the
	// hidden defaults in its body's call, as the callee's own call does.
	// It holds no supplier for a computed default, since a call leaving
	// one out goes through the entry.
	lam, slots, ok := bl.partialLam(pc, nil, false)
	if !ok {
		return no()
	}
	args, ok := bl.partialBodyArgs(pc, filled)
	if !ok {
		return no()
	}
	v, k, mobile, ok := bl.partialLambda(capture, lam, t, args, slots)
	if !ok {
		return no()
	}
	if lp := bl.callablePlan(v); lp != nil {
		lp.entry, lp.entryKind, lp.entryGroup = entry, ek, entryPlan.group
	}
	// With no plan the value is a signalling callback's adapter, which its
	// operation calls with every argument.
	return v, k, mobile, true
}

// partialOverEntry is t with its callee replaced by that callee's entry when
// the callee is a local partial that has one (`&g(&1, c: &2)` over `g =
// &span(&1, c: &2)`): the partial over g is a partial over g's entry, whose
// parameters are g's and then g's hidden slots, so g's hidden defaults are
// this partial's hidden slots in turn, and each runs once a call. The
// entry is bound under a name no program can spell. t otherwise.
func (bl *irScalarBuilder) partialOverEntry(t *ast.Call) *ast.Call {
	id, direct := t.Func.(*ast.Ident)
	if !direct || !bl.isLocalCallable(id.Name) {
		return t
	}
	v, _, _, ok := bl.lower(id)
	if !ok {
		return t
	}
	plan := bl.callablePlan(v)
	if plan == nil || plan.entryGroup == nil {
		return t
	}
	name := "%entry" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + id.Name
	bl.bound[name], bl.boundK[name] = plan.entry, plan.entryKind
	return &ast.Call{Func: &ast.Ident{Name: name, Line: id.Line, Col: id.Col}, Args: t.Args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col}
}

// partialCall is what partialApplication knows of a partial capture's call
// t: the callee's parameters and kinds, its signature when it is a function
// of this file, the call's slot plan and, per callee slot, the written
// argument, whether the call fills it, its placeholder and the name its
// lambda parameter takes.
type partialCall struct {
	t           *ast.Call
	capture     *ast.Capture
	params      []ast.Param
	kinds       []kind
	sig         *fnSig
	names       []string
	plan        argPlan
	written     []ast.Node
	filled      []bool
	placeholder []ast.Node
	openName    []string
	calleeOrder []int
	open        []int
	extra       []kind
}

// partialHiddenSlots is the slots the partial leaves unmentioned whose
// default is computed, each before, in the callee's order, an open slot
// whose default is computed: `b` in `&span(&1, c: &2)` over `fn span(a,
// b = base() + 1, c = base() * 2)`. A call of the partial that leaves `c`
// out evaluates b's default and then c's, and the call in the partial's
// body must not evaluate b's again.
func partialHiddenSlots(params []ast.Param, filled []bool, open, calleeOrder []int) []int {
	computed := func(s int) bool { return params[s].Default != nil && !irClosedLiteral(params[s].Default) }
	last := -1
	for at, s := range calleeOrder {
		for _, o := range open {
			if o == s && computed(s) {
				last = at
			}
		}
	}
	var hidden []int
	for at, s := range calleeOrder {
		if at < last && !filled[s] && computed(s) {
			hidden = append(hidden, s)
		}
	}
	return hidden
}

// partialLam builds the lambda a partial stands for: a parameter per open
// slot in written order, then the ones the expected type adds, which it
// ignores, then one per hidden slot, which no call can name. With
// suppliers, an open or hidden slot whose callee default is computed gets
// the callee's default as its supplier (partialCalleeDefault); without, it
// has none, and only a literal default carries over.
func (bl *irScalarBuilder) partialLam(pc partialCall, hidden []int, suppliers bool) (*ast.Lambda, partialSlots, bool) {
	t := pc.t
	lam := &ast.Lambda{Line: t.Line, Col: t.Col, EndLine: t.Line, EndCol: t.Col}
	n := len(pc.open) + len(pc.extra) + len(hidden)
	slots := partialSlots{kinds: make([]kind, 0, n), names: make([]string, 0, n)}
	// lamSlot is each parameter's callee slot, -1 for an ignored one.
	lamSlot := make([]int, 0, n)
	add := func(slot int, name string, dflt, marker ast.Node) bool {
		p := pc.params[slot]
		if dflt != nil && !irClosedLiteral(dflt) {
			if !suppliers {
				dflt = nil
			} else {
				fill, ok := bl.partialCalleeDefault(t, pc.params, pc.sig, slot, pc.kinds, pc.written, pc.filled, pc.openName, pc.calleeOrder)
				if !ok {
					return false
				}
				if slots.defaults == nil {
					slots.defaults = map[int]func(*irScalarBuilder) (ir.Temp, bool){}
				}
				slots.defaults[len(lam.Params)] = fill
				// The supplier answers fill's value; the default node only
				// gives the parameter one.
				dflt = marker
			}
		}
		lam.Params = append(lam.Params, ast.Param{Name: pc.openName[slot], Default: dflt, Line: p.Line, Col: p.Col})
		slots.kinds = append(slots.kinds, pc.kinds[slot])
		if p.Destructure != nil || name == "_" {
			// A pattern or a bare `_` is no name a call can write.
			name = ""
		}
		slots.names = append(slots.names, name)
		lamSlot = append(lamSlot, slot)
		return true
	}
	for _, slot := range pc.open {

		if !add(slot, pc.params[slot].Name, pc.params[slot].Default, pc.placeholder[slot]) {
			return nil, partialSlots{}, false
		}
	}
	for i, k := range pc.extra {
		lam.Params = append(lam.Params, ast.Param{Name: captureParamName(pc.capture, len(pc.open)+i+1), Line: t.Line, Col: t.Col})
		slots.kinds = append(slots.kinds, k)
		slots.names = append(slots.names, "")
		lamSlot = append(lamSlot, -1)
	}
	for _, slot := range hidden {
		if !add(slot, "", pc.params[slot].Default, &ast.Placeholder{Line: t.Line, Col: t.Col}) {
			return nil, partialSlots{}, false
		}
	}
	slots.order = partialFillOrder(lamSlot, pc.calleeOrder)
	if slots.order != nil {
		// An ignored parameter has no default and is filled last.
		for j, slot := range lamSlot {
			if slot < 0 {
				slots.order = append(slots.order, j)
			}
		}
	}
	return lam, slots, true
}

// partialBodyArgs is the arguments of the call in a partial's body, given
// which callee slots its lambda's parameters fill.
func (bl *irScalarBuilder) partialBodyArgs(pc partialCall, filled []bool) ([]ast.Node, bool) {
	t, params, names := pc.t, pc.params, pc.names
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
	planned := make([]bool, len(params))
	for _, i := range pc.plan.order {
		slot := pc.plan.slots[i]
		planned[slot] = true
		if pc.written[slot] != nil {
			inOrder = inOrder && slot > prevSlot
			prevSlot = slot
		}
		nameable = nameable && names[slot] != "" && names[slot] != "_"
	}
	for slot := range params {
		if filled[slot] && !planned[slot] {
			// A hidden slot, which the entry's call passes by name.
			nameable = nameable && names[slot] != "" && names[slot] != "_"
		}
	}
	if !inOrder && nameable {
		var args []ast.Node
		for _, i := range pc.plan.order {
			slot := pc.plan.slots[i]
			arg := pc.written[slot]
			if arg == nil {
				arg = &ast.Ident{Name: pc.openName[slot], Line: t.Line, Col: t.Col}
			}
			args = append(args, &ast.NamedArg{Name: names[slot], Value: arg, Line: t.Line, Col: t.Col})
		}
		for slot := range params {
			if filled[slot] && !planned[slot] {
				arg := &ast.Ident{Name: pc.openName[slot], Line: t.Line, Col: t.Col}
				args = append(args, &ast.NamedArg{Name: names[slot], Value: arg, Line: t.Line, Col: t.Col})
			}
		}
		return args, true
	}
	var args []ast.Node
	for slot, p := range params[:last+1] {
		line, col := t.Line, t.Col
		var arg ast.Node
		switch {
		case pc.written[slot] != nil:
			arg = pc.written[slot]
		case filled[slot]:
			arg = &ast.Ident{Name: pc.openName[slot], Line: line, Col: col}
		case named && p.Default != nil:
			continue
		case p.Default != nil && irClosedLiteral(p.Default):
			arg = p.Default
		default:
			bl.g.dc.note("a partial's body call over an unmentioned slot with no default")
			return nil, false
		}
		if named {
			if names[slot] == "" || names[slot] == "_" {
				bl.g.dc.note("a partial's body call naming a slot whose parameter has no name")
				return nil, false
			}
			arg = &ast.NamedArg{Name: names[slot], Value: arg, Line: line, Col: col}
		}
		args = append(args, arg)
	}
	return args, true
}

// partialEntry lowers lam, whose body is the call t with args, as a
// partial's entry: a plain function value a call of the partial that leaves
// an argument out goes through (irLambdaPlan.entry).
func (bl *irScalarBuilder) partialEntry(lam *ast.Lambda, t *ast.Call, args []ast.Node, slots partialSlots) (ir.Temp, kind, bool, bool) {
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

// partialLambda lowers lam, whose body is the call t with args, as the
// function a partial capture stands for, its parameters taking slots.
func (bl *irScalarBuilder) partialLambda(capture *ast.Capture, lam *ast.Lambda, t *ast.Call, args []ast.Node, slots partialSlots) (ir.Temp, kind, bool, bool) {
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{
		&ast.Call{Func: t.Func, Args: args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col},
	}}
	if bl.partialOpen == nil {
		bl.partialOpen = map[*ast.Lambda]partialSlots{}
	}
	bl.partialOpen[lam] = slots
	defer delete(bl.partialOpen, lam)
	return bl.lambdaOfCapture(capture, lam)
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
// does (indirectCall). An earlier slot the partial leaves unmentioned has
// a literal default, or is a hidden slot of the partial's entry, which the
// supplier reads as it reads an open one.
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
	if sig == nil && !value && sibling == nil {
		if fill, ok := bl.partialStdDefault(t, kinds, slot, written, filled, openName); ok {
			return fill, true
		}
	}
	if (sig == nil && !value && sibling == nil) || (sig != nil && len(sig.decl.Params) != len(kinds)) {
		bl.g.dc.note("a partial's open slot whose default is not a literal, over a callee outside this file's functions")
		return nil, false
	}
	// The slots the callee fills before this one. A placeholder among them
	// may be written after this one (`&span(b: &1, &2)`): the partial fills
	// its parameters in the callee's order (partialFillOrder), so this
	// slot's supplier takes it.
	before := slotsBefore(calleeOrder, slot)
	for _, s := range before {
		if p := params[s]; !filled[s] && p.Default != nil && !irClosedLiteral(p.Default) {
			// partialApplication makes every such slot a hidden one of the
			// partial's entry (partialHiddenSlots), which fills it.
			panic("irbuild: a partial's computed default after an unmentioned computed one that is not hidden")
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

// partialStdDefault is partialCalleeDefault for a std function's slot
// (`style` in `&String.join_list(names, locale, style: &1)`): the supplier
// calls the accessor the declaring module built for the slot's default
// (emitStdSlotDefaults) with every slot before it, as a call that leaves the
// slot out by name does (stdShortCallPlan). A slot before it that the
// partial leaves unmentioned takes its own accessor first, in ascending
// order, so each default runs once. ok is false when the callee is no std
// function with such accessors.
func (bl *irScalarBuilder) partialStdDefault(t *ast.Call, kinds []kind, slot int, written []ast.Node, filled []bool, openName []string) (func(*irScalarBuilder) (ir.Temp, bool), bool) {
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || bl.g.std == nil {
		return nil, false
	}
	owner, module, ok := bl.stdShortOwner(fa)
	if !ok {
		return nil, false
	}
	if module == "" {
		// A bare owner the program declares itself names the program's type.
		if _, local := bl.g.types[owner]; local {
			return nil, false
		}
		if _, local := bl.g.ifaces[owner]; local {
			return nil, false
		}
	}
	fs := bl.g.stdShortCandidates(owner, fa.Field.Name, module)
	if len(fs) != 1 || !fs[0].lowerable() {
		return nil, false
	}
	f := fs[0]
	canon := f
	if f.canon != nil {
		canon = f.canon
	}
	if len(f.params) != len(kinds) {
		return nil, false
	}
	for s := 0; s <= slot; s++ {
		if s < len(kinds) && kinds[s] != f.params[s] {
			return nil, false
		}
		if written[s] == nil && !filled[s] && (!callDefaultFill(f, s) || canon.irSlot[s] == nil) {
			return nil, false
		}
		if !irSlotSignature(f.params[:s], f.params[:s], f.params[s]) {
			return nil, false
		}
	}
	return func(child *irScalarBuilder) (ir.Temp, bool) {
		args := irQualArgs{temps: make([]ir.Temp, slot), kinds: make([]kind, slot), mobile: make([]bool, slot), ok: true}
		accessor := func(s int) (ir.Temp, bool) {
			prefix := irQualArgs{temps: args.temps[:s], kinds: args.kinds[:s], mobile: args.mobile[:s], ok: true}
			p := &irQualPlan{token: irSlotDefault{f, s}, name: fmt.Sprintf("%s default %d", f.key, s+1), result: f.params[s], sym: canon.irSlot[s].Sym()}
			v, _, _, ok := child.qualEmit(t, prefix, p)
			return v, ok
		}
		for s := range slot {
			var v ir.Temp
			var k kind
			ok := true
			switch {
			case written[s] != nil:
				// A written argument the default may read, evaluated for
				// it on each call that omits the slot.
				v, k, _, ok = child.lowerTypedOperand(written[s], kinds[s])
				if ok && k != kinds[s] {
					v, k, ok = child.coerceEmpty(written[s], v, k, kinds[s])
				}
			case filled[s]:
				v, k, _, ok = child.lower(&ast.Ident{Name: openName[s], Line: t.Line, Col: t.Col})
			default:
				v, ok = accessor(s)
				k = f.params[s]
			}
			if !ok || k != kinds[s] {
				return ir.NoTemp, false
			}
			args.temps[s], args.kinds[s] = v, k
		}
		return accessor(slot)
	}, true
}

// partialSiblingDefaults is how a partial over another file's function
// (`&lib.conn("h", &1)`, or `conn` after `import lib.{conn}`) fills the
// slots up to slot it omits: with the accessors the declaring file builds
// for its defaults, as a call omitting them does (fillFromAccessors), over
// the slots up to slot so no later default runs. nil when the
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
		params, result, sym, ok = bl.g.siblingGenericInstance(t.Func, site.unit, f)
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
	owner := bl.g.unitGen(site.unit)
	if f.decl.Params[slot].Default == nil {
		return nil
	}
	accessor := func(s int) *ir.Symbol {
		if f.decl.Params[s].Default == nil {
			return nil
		}
		return owner.irFileSlotSym(plan.sym, f.decl.Name, s)
	}
	return func(child *irScalarBuilder, args []ir.Temp) bool {
		return child.fillFromAccessors(t, kinds[:slot+1], args, nil, accessor)
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
	inst, ok := bl.g.monoInstanceAt(t.Func, tpl, callee.Name)
	if !ok || inst.sig == nil || !inst.sig.lowerable || inst.sig.decl == nil {
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
	kinds, _, ok := bl.g.checkedValueKinds(t.Func)
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
