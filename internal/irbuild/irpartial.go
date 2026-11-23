package irbuild

import (
	"fmt"
	"slices"
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
// file's function. Arguments reach the slots the checker recorded (callPlan);
// a slot given twice declines.
//
// Each open argument becomes a parameter of the type the checker gave the
// capture there, the value its position passes, which enters the callee's
// slot as a written argument of that type does (the view the checker
// records at the `&N`, which the body's call reads through openArg). It is
// named so no Nomi program can spell it, so no parameter shadows a name the
// callee expression starts from
// (`&option.Option.map(&1, f)`, whose first parameter is `option`). It carries
// the callee's default when the builder can see the declaration, so an
// argument the call of the partial omits takes it, and partialOpen hands the
// lambda the slot's kind and the callee's parameter name, by which the
// partial is called (`f(port: 3000)`). A slot the partial does not mention
// takes the callee's default in the body's call, which leaves the slot out.
// An open slot's computed default is the callee's, evaluated in the callee's
// scope by the slot's supplier (partialCalleeDefault). When the partial
// also writes an argument (`say(1)` in `&f(say(1), &1, &2)`), or leaves a
// computed default unmentioned before that slot (`b` in `&span(&1, c: &2)`
// over `fn span(a, b = base() + 1, c = base() * 2)`), it also gets an
// entry (irLambdaPlan.entry) that takes those held and hidden slots after
// its own: a call that leaves an argument out goes through the entry,
// evaluates each written argument once, then every default it leaves out
// once, in the callee's order, reading the written values; a call that
// passes every argument goes through the partial, whose body's call
// evaluates the written arguments and the hidden defaults. Each route to the callee's defaults
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
	plan, planned := bl.g.callPlan(t, len(kinds))
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
	// open slot's computed default comes after (partialHiddenSlots), and,
	// when an open slot's default is computed, the written arguments
	// (partialHeldSlots). A call of the partial that leaves an argument out
	// goes through the partial's entry, which takes these too: it evaluates
	// each written argument once, and then each default it leaves out
	// once, in the callee's order, reading those values.
	_, iface := bl.g.calleeDecl(t.Func).(*ast.InterfaceMethod)
	pc := partialCall{params: params, iface: iface && sig == nil}
	hidden := partialHiddenSlots(pc, filled, open, calleeOrder)
	held := partialHeldSlots(pc, open, written, plan)
	openName := make([]string, len(params))
	for _, slot := range open {
		openName[slot] = "%open" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
	}
	for _, slot := range hidden {
		openName[slot] = "%hidden" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
	}
	for _, slot := range held {
		openName[slot] = "%held" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(slot)
	}
	// Each slot's local in the partial's lambda, by a key of its own: the
	// lambda's parameters stand at the callee's parameters' positions.
	openKey := make([]analysis.Pos, len(params))
	for slot, name := range openName {
		if name != "" {
			openKey[slot] = bl.g.madeLocal()
		}
	}
	// The open arguments take the parameters of the function type the
	// checker gave the capture, the values its position passes, which
	// enter the callee's slots as written arguments of those types do:
	// `&Iter.sum(&1)` over a List of Lists takes each List, which the
	// body's call views as its Iter<Int> slot. The parameters the position
	// expects past the open arguments the function ignores.
	ft, _ := analysis.ResolveClosed(bl.g.srcFA().ExprTypes[capture]).(*analysis.FuncType)
	if ft == nil || len(ft.Params) < len(open) {
		// The checker types every capture it accepts as a function of at
		// least its open arguments (captureFunc's own check says so).
		panic("irbuild: a partial capture the checker gave no function type of its open arguments")
	}
	openKinds := make([]kind, len(open))
	for i := range open {
		openKinds[i] = bl.g.project(ft.Params[i])
	}
	var extra []kind
	for i := len(open); i < len(ft.Params); i++ {
		k := bl.g.project(ft.Params[i])
		irHeldOperand(k, "a partial application's ignored parameter")
		extra = append(extra, k)
	}
	pc = partialCall{t: t, capture: capture, params: params, kinds: kinds, sig: sig, iface: pc.iface, names: names, plan: plan,
		written: written, filled: filled, placeholder: placeholder, openName: openName, openKey: openKey, calleeOrder: calleeOrder, open: open,
		openKinds: openKinds, extra: extra}
	if len(hidden) == 0 && len(held) == 0 {
		lam, slots, ok := bl.partialLam(pc, nil, nil, true)
		if !ok {
			return no()
		}
		args, placed, ok := bl.partialBodyArgs(pc, filled)
		if !ok {
			return no()
		}
		return bl.partialLambda(capture, lam, t, args, placed, slots)
	}
	// The entry: the open slots, the ignored ones, then the held ones and
	// the hidden ones, with each written argument's supplier and every
	// computed default's. Its body's call reads a written argument as the
	// held slot its supplier filled.
	withHidden := append([]bool(nil), filled...)
	for _, slot := range hidden {
		withHidden[slot] = true
	}
	entryPC := pc
	entryPC.filled = withHidden
	entryPC.written = make([]ast.Node, len(params))
	entryLam, entrySlots, ok := bl.partialLam(entryPC, append(held, hidden...), written, true)
	if !ok {
		return no()
	}
	entryArgs, entryPlaced, ok := bl.partialBodyArgs(entryPC, withHidden)
	if !ok {
		return no()
	}
	entry, ek, _, ok := bl.partialEntry(capture, entryLam, t, entryArgs, entryPlaced, entrySlots)
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
	lam, slots, ok := bl.partialLam(pc, nil, nil, false)
	if !ok {
		return no()
	}
	args, placed, ok := bl.partialBodyArgs(pc, filled)
	if !ok {
		return no()
	}
	v, k, mobile, ok := bl.partialLambda(capture, lam, t, args, placed, slots)
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
// entry is bound under a name no program can spell. The call over the
// entry holds t's arguments at t's position, so it has t's record, which
// leaves the hidden slots after g's own out. t otherwise.
func (bl *irScalarBuilder) partialOverEntry(t *ast.Call) *ast.Call {
	id, direct := t.Func.(*ast.Ident)
	if !direct || !bl.isLocalCallable(id) {
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
	key := bl.g.madeLocal()
	bl.declareKey(key, plan.entry, plan.entryKind)
	return &ast.Call{Func: bl.g.madeRead(name, key, id.Line, id.Col), Args: t.Args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col}
}

// partialCall is what partialApplication knows of a partial capture's call
// t: the callee's parameters and kinds, its signature when it is a function
// of this file, the call's slot plan and, per callee slot, the written
// argument, whether the call fills it, its placeholder and the name its
// lambda parameter takes. iface is set when the callee is an interface
// function, whose defaults are the interface's: the function a call of the
// partial runs may declare its own (spec §12 *Item kinds*).
type partialCall struct {
	t           *ast.Call
	capture     *ast.Capture
	params      []ast.Param
	kinds       []kind
	sig         *fnSig
	iface       bool
	names       []string
	plan        argPlan
	written     []ast.Node
	filled      []bool
	placeholder []ast.Node
	openName    []string
	openKey     []analysis.Pos
	calleeOrder []int
	open        []int
	// openKinds is each open slot's kind, in open's order: the parameter
	// the checker gave the capture there.
	openKinds []kind
	extra     []kind
}

// literal reports whether d, the default of the callee's parameter at slot
// or an argument the partial writes there, reads the same written in the
// partial: a closed literal (irClosedLiteral) that is not an interface
// function's default, which the impl a call reaches may replace.
func (pc partialCall) literal(slot int, d ast.Node) bool {
	return irClosedLiteral(d) && !(pc.iface && d == pc.params[slot].Default)
}

// computed reports whether the callee's parameter at slot has a default
// that is not literal.
func (pc partialCall) computed(slot int) bool {
	d := pc.params[slot].Default
	return d != nil && !pc.literal(slot, d)
}

// partialHiddenSlots is the slots the partial leaves unmentioned whose
// default is computed, each before, in the callee's order, an open slot
// whose default is computed: `b` in `&span(&1, c: &2)` over `fn span(a,
// b = base() + 1, c = base() * 2)`. A call of the partial that leaves `c`
// out evaluates b's default and then c's, and the call in the partial's
// body must not evaluate b's again.
func partialHiddenSlots(pc partialCall, filled []bool, open, calleeOrder []int) []int {
	last := -1
	for at, s := range calleeOrder {
		for _, o := range open {
			if o == s && pc.computed(s) {
				last = at
			}
		}
	}
	var hidden []int
	for at, s := range calleeOrder {
		if at < last && !filled[s] && pc.computed(s) {
			hidden = append(hidden, s)
		}
	}
	return hidden
}

// partialHeldSlots is the slots of the written arguments, in the order the
// partial's call evaluates them, when an open slot's default is computed:
// `a` in `&f(say(1), &1, &2)` over `fn f(a: Int, b: Int, c: Int = a * 2)`.
// A call of the partial that leaves `c` out evaluates `say(1)` once and
// then c's default, which reads its value (spec §7 *Default Arguments*).
// nil otherwise.
func partialHeldSlots(pc partialCall, open []int, written []ast.Node, plan argPlan) []int {
	computed := false
	for _, o := range open {
		computed = computed || pc.computed(o)
	}
	if !computed {
		return nil
	}
	var held []int
	for _, i := range plan.order {
		if slot := plan.slots[i]; written[slot] != nil {
			held = append(held, slot)
		}
	}
	return held
}

// partialLam builds the lambda a partial stands for: a parameter per open
// slot in written order, then the ones the expected type adds, which it
// ignores, then one per hidden slot, which no call can name. A hidden slot
// with an expression in held holds that written argument: its supplier
// evaluates it, before every other supplier (partialFillOrder), so each
// default reads its value and none evaluates it again. With suppliers, an
// open or hidden slot whose callee default is computed gets the callee's
// default as its supplier (partialCalleeDefault); without, it has none,
// and only a literal default carries over.
func (bl *irScalarBuilder) partialLam(pc partialCall, hidden []int, held []ast.Node, suppliers bool) (*ast.Lambda, partialSlots, bool) {
	t := pc.t
	lam := &ast.Lambda{Line: t.Line, Col: t.Col, EndLine: t.Line, EndCol: t.Col}
	n := len(pc.open) + len(pc.extra) + len(hidden)
	slots := partialSlots{kinds: make([]kind, 0, n), names: make([]string, 0, n)}
	// lamSlot is each parameter's callee slot, -1 for an ignored one, and
	// keys each parameter's key (madeParams).
	lamSlot := make([]int, 0, n)
	keys := make([]analysis.Pos, 0, n)
	add := func(slot int, k kind, name string, dflt, marker ast.Node) bool {
		p := pc.params[slot]
		if dflt != nil && !pc.literal(slot, dflt) {
			if !suppliers {
				dflt = nil
			} else {
				fill, ok := partialWrittenArg(held, slot, pc.kinds[slot])
				if fill == nil {
					fill, ok = bl.partialCalleeDefault(t, pc.params, pc.sig, slot, pc.kinds, pc.filled, pc.openName, pc.openKey, pc.calleeOrder)
				}
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
		keys = append(keys, pc.openKey[slot])
		slots.kinds = append(slots.kinds, k)
		if p.Destructure != nil || name == "_" {
			// A pattern or a bare `_` is no name a call can write.
			name = ""
		}
		slots.names = append(slots.names, name)
		lamSlot = append(lamSlot, slot)
		return true
	}
	for i, slot := range pc.open {
		if !add(slot, pc.openKinds[i], pc.params[slot].Name, pc.params[slot].Default, pc.placeholder[slot]) {
			return nil, partialSlots{}, false
		}
	}
	for i, k := range pc.extra {
		lam.Params = append(lam.Params, ast.Param{Name: captureParamName(pc.capture, len(pc.open)+i+1), Line: t.Line, Col: t.Col})
		keys = append(keys, bl.g.madeLocal())
		slots.kinds = append(slots.kinds, k)
		slots.names = append(slots.names, "")
		lamSlot = append(lamSlot, -1)
	}
	for _, slot := range hidden {
		dflt := pc.params[slot].Default
		if held != nil && held[slot] != nil {
			dflt = held[slot]
		}
		if !add(slot, pc.kinds[slot], "", dflt, &ast.Placeholder{Line: t.Line, Col: t.Col}) {
			return nil, partialSlots{}, false
		}
	}
	slots.order = partialFillOrder(lamSlot, held, pc.calleeOrder)
	bl.g.setMadeParams(lam, keys)
	return lam, slots, true
}

// partialWrittenArg is the supplier of a held slot (partialLam): the
// written argument, evaluated in the partial's scope at the slot's kind, as
// the body's call of the partial evaluates it. nil when slot holds no
// written argument.
func partialWrittenArg(held []ast.Node, slot int, k kind) (func(*irScalarBuilder) (ir.Temp, bool), bool) {
	if held == nil || held[slot] == nil {
		return nil, false
	}
	arg := held[slot]
	return func(child *irScalarBuilder) (ir.Temp, bool) {
		v, vk, _, ok := child.lowerTypedOperand(arg, k)
		if ok && vk != k {
			v, vk, ok = child.convertTo(arg, v, vk, k)
		}
		return v, ok && vk == k
	}, true
}

// partialBodyArgs is the arguments of the call in a partial's body, given
// which callee slots its lambda's parameters fill, and the record of that
// call: the builder writes each argument for a slot, so the record says
// which.
func (bl *irScalarBuilder) partialBodyArgs(pc partialCall, filled []bool) ([]ast.Node, *analysis.ResolvedCall, bool) {
	t, params, names := pc.t, pc.params, pc.names
	// A slot the partial does not mention is left out of the body's call,
	// which fills it from the callee's default in the callee's scope, as any
	// call omitting it does. A trailing one is simply not written; one
	// before a written slot is skipped by writing the call's arguments by
	// name.
	last := -1
	named := false
	for slot := range params {
		if filled[slot] {
			last = slot
		}
	}
	for slot := range params[:last+1] {
		if !filled[slot] {
			named = true
		}
	}
	// The written arguments are evaluated in the order the partial's call
	// evaluates them (callPlan's order, as written), as the equivalent lambda's call
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
	var args []ast.Node
	var argSlots []int
	if !inOrder && nameable {
		for _, i := range pc.plan.order {
			slot := pc.plan.slots[i]
			arg := pc.written[slot]
			if arg == nil {
				arg = bl.openArg(pc, slot)
			}
			args = append(args, &ast.NamedArg{Name: names[slot], Value: arg, Line: t.Line, Col: t.Col})
			argSlots = append(argSlots, slot)
		}
		for slot := range params {
			if filled[slot] && !planned[slot] {
				arg := bl.openArg(pc, slot)
				args = append(args, &ast.NamedArg{Name: names[slot], Value: arg, Line: t.Line, Col: t.Col})
				argSlots = append(argSlots, slot)
			}
		}
		return args, writtenAtSlots(argSlots, len(params)), true
	}
	for slot, p := range params[:last+1] {
		line, col := t.Line, t.Col
		var arg ast.Node
		switch {
		case pc.written[slot] != nil:
			arg = pc.written[slot]
		case filled[slot]:
			arg = bl.openArg(pc, slot)
		case named && p.Default != nil:
			continue
		default:
			bl.g.dc.note("a partial's body call over an unmentioned slot with no default")
			return nil, nil, false
		}
		if named {
			if names[slot] == "" || names[slot] == "_" {
				bl.g.dc.note("a partial's body call naming a slot whose parameter has no name")
				return nil, nil, false
			}
			arg = &ast.NamedArg{Name: names[slot], Value: arg, Line: line, Col: col}
		}
		args = append(args, arg)
		argSlots = append(argSlots, slot)
	}
	return args, writtenAtSlots(argSlots, len(params)), true
}

// openArg is the name the call in a partial's body reads its open or
// hidden slot slot as. An open one stands for the capture's `&N`, whose
// records it reads (holeFor): the view the checker records where the value
// the position passes enters the callee's slot.
func (bl *irScalarBuilder) openArg(pc partialCall, slot int) ast.Node {
	arg := bl.g.madeRead(pc.openName[slot], pc.openKey[slot], pc.t.Line, pc.t.Col)
	if ph := pc.placeholder[slot]; ph != nil {
		if bl.g.holeFor == nil {
			bl.g.holeFor = map[ast.Node]ast.Node{}
		}
		bl.g.holeFor[arg] = ph
	}
	return arg
}

// writtenAtSlots is the record of a call the builder writes, whose
// argument i fills argSlots[i] of the callee's params parameters and which
// leaves every other parameter to its default: the call in a partial's
// body, which writes no argument for a slot the partial leaves unmentioned.
func writtenAtSlots(argSlots []int, params int) *analysis.ResolvedCall {
	rc := &analysis.ResolvedCall{Slots: make([]analysis.CallSlot, params), Order: make([]int, len(argSlots))}
	for i := range rc.Slots {
		rc.Slots[i].Fill = analysis.SlotDefault
	}
	for i, slot := range argSlots {
		rc.Slots[slot] = analysis.CallSlot{Fill: analysis.SlotArg, Arg: i}
		rc.Order[i] = i
	}
	return rc
}

// partialEntry lowers lam, whose body is the call t with args, as the
// entry of the partial capture: a plain function value a call of the
// partial that leaves an argument out goes through (irLambdaPlan.entry).
// It takes more parameters than the capture and answers what the capture
// answers, so lam carries the capture's type (carryType) for its result.
func (bl *irScalarBuilder) partialEntry(capture *ast.Capture, lam *ast.Lambda, t *ast.Call, args []ast.Node, placed *analysis.ResolvedCall, slots partialSlots) (ir.Temp, kind, bool, bool) {
	call := &ast.Call{Func: t.Func, Args: args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col}
	placed.Callee = bl.g.calleeOf(t)
	defer bl.g.carryCall(call, placed)()
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{call}}
	if bl.partialOpen == nil {
		bl.partialOpen = map[*ast.Lambda]partialSlots{}
	}
	bl.partialOpen[lam] = slots
	defer delete(bl.partialOpen, lam)
	defer bl.g.carryType(lam, bl.g.recordedType(capture))()
	return bl.lambda(lam)
}

// partialLambda lowers lam, whose body is the call t with args, as the
// function a partial capture stands for, its parameters taking slots.
func (bl *irScalarBuilder) partialLambda(capture *ast.Capture, lam *ast.Lambda, t *ast.Call, args []ast.Node, placed *analysis.ResolvedCall, slots partialSlots) (ir.Temp, kind, bool, bool) {
	call := &ast.Call{Func: t.Func, Args: args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col}
	placed.Callee = bl.g.calleeOf(t)
	defer bl.g.carryCall(call, placed)()
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{call}}
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
	if callee, direct := t.Func.(*ast.Ident); direct && bl.isLocalCallable(callee) {
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

// partialFillOrder is the order a call fills the partial's parameters
// (lamSlot holds each one's callee slot, -1 for an ignored one): the held
// written arguments first, in the order the partial's call evaluates them,
// then the rest in the order the callee fills their slots, then the
// ignored ones, which have no default; nil when that is the parameters'
// own order.
func partialFillOrder(lamSlot []int, held []ast.Node, calleeOrder []int) []int {
	isHeld := func(s int) bool { return s >= 0 && held != nil && held[s] != nil }
	var order []int
	for j, s := range lamSlot {
		if isHeld(s) {
			order = append(order, j)
		}
	}
	for _, slot := range calleeOrder {
		for j, s := range lamSlot {
			if s == slot && !isHeld(s) {
				order = append(order, j)
			}
		}
	}
	for j, s := range lamSlot {
		if s < 0 {
			order = append(order, j)
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
// supplier's own parameters: the open slots before it and the written
// arguments, which the partial's entry holds (partialHeldSlots), so the
// supplier reads their values and evaluates none of them again. The callee is a function of
// this file (sig, or the generic instance partialMonoSig finds), or a local
// function value whose default suppliers the builder holds: the supplier
// captures the value's suppliers as any closure calling it does
// (capturedPlan) and calls the slot's one, as a call omitting the slot
// does (indirectCall). An earlier slot the partial leaves unmentioned has
// a literal default, or is a hidden slot of the partial's entry, which the
// supplier reads as it reads an open one.
func (bl *irScalarBuilder) partialCalleeDefault(t *ast.Call, params []ast.Param, sig *fnSig, slot int, kinds []kind, filled []bool, openName []string, openKey []analysis.Pos, calleeOrder []int) (func(*irScalarBuilder) (ir.Temp, bool), bool) {
	callee, direct := t.Func.(*ast.Ident)
	value := direct && bl.isLocalCallable(callee)
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
	if im, iface := bl.g.calleeDecl(t.Func).(*ast.InterfaceMethod); iface && sig == nil && !value {
		// nil for a slot before the receiver, which declines below.
		sibling = bl.partialIfaceDefaults(t, im, kinds, slot, calleeOrder)
	}
	if sig == nil && !value && sibling == nil {
		sibling = bl.partialSiblingDefaults(t, kinds, slot)
	}
	if sig == nil && !value && sibling == nil {
		if fill, ok := bl.partialStdDefault(t, kinds, slot, filled, openName, openKey); ok {
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
			if !filled[s] {
				continue
			}
			v, k, _, ok := child.lower(child.g.madeRead(openName[s], openKey[s], t.Line, t.Col))
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
	if direct && bl.isLocalCallable(callee) {
		v, fk, _, ok := bl.lower(callee)
		if !ok {
			return nil, nil, nil, false
		}
		if fk.tag != tagFunc {
			// isLocalCallable read the name's kind from the scope the read
			// answers from.
			panic("irbuild: a local callable read at the kind " + fk.nomi())
		}
		plan := bl.callablePlan(v)
		if plan == nil || plan.source == nil || len(plan.names) != len(plan.source.Params) {
			// A value with no lambda behind it in view: the checker's
			// record of the call names the function it is.
			return bl.partialCalleeRecorded(t, funcParams(fk))
		}
		// The names a call of the value writes are the plan's: a partial's
		// lambda names its own parameters so no program can spell them.
		params := make([]ast.Param, len(plan.source.Params))
		for i, p := range plan.source.Params {
			params[i] = ast.Param{Name: plan.names[i], Default: p.Default, Line: p.Line, Col: p.Col}
		}
		return params, funcParams(fk), nil, true
	}
	if target := bl.g.calleeOf(t); direct && target.Kind != analysis.CalleeValue && bl.g.stdSiblingDecls[target.Decl] == nil {
		if sig := bl.g.sigByDecl[target.Decl]; sig != nil && sig.lowerable && sig.decl != nil && sig.dict == nil && sig.tps == nil {
			return sig.decl.Params, sig.params, sig, true
		}
	}
	params, kinds, ok := bl.partialCalleeByType(t)
	return params, kinds, nil, ok
}

// partialCalleeRecorded is partialCallee for a local function value the
// builder holds no lambda for, from the callee the checker recorded for the
// call (analysis.Callee): a nested `fn` named inside its own body, whose
// declaration holds the parameters, or a local copy of a declared function
// (`g = sub`), which is that function's partial. Any other value has only
// its kind's parameters. kinds are the value's parameter kinds.
func (bl *irScalarBuilder) partialCalleeRecorded(t *ast.Call, kinds []kind) ([]ast.Param, []kind, *fnSig, bool) {
	rec := bl.g.resolvedCallOf(t)
	if rec == nil {
		panic("irbuild: a partial application with no record of its call")
	}
	if rec.Callee.Kind == analysis.CalleeValue {
		if fd, ok := rec.Callee.Decl.(*ast.FuncDef); ok && rec.Callee.Copy == nil && len(fd.Params) == len(kinds) {
			return fd.Params, kinds, nil, true
		}
	}
	if rec.Callee.Kind == analysis.CalleeValue && rec.Callee.Copy != nil {
		// The copy's partial is the copied function's: the same call, at
		// the same position and over the same arguments, so it has the
		// same record.
		over := &ast.Call{Func: rec.Callee.Copy, Args: t.Args, TypeArgs: t.TypeArgs, Line: t.Line, Col: t.Col}
		if id, stillLocal := over.Func.(*ast.Ident); !stillLocal || !bl.isLocalCallable(id) {
			return bl.partialCallee(over)
		}
	}
	// A function value with no function in view behind it (a call's
	// result, a parameter): its parameters have no names and no defaults.
	return make([]ast.Param, len(kinds)), kinds, nil, true
}

// partialStdDefault is partialCalleeDefault for a std function's slot
// (`style` in `&String.join_list(names, locale, style: &1)`): the supplier
// calls the accessor the declaring module built for the slot's default
// (emitStdSlotDefaults) with every slot before it, as a call that leaves the
// slot out by name does (stdShortCallPlan): an open or held slot before it
// is the supplier's parameter, as in partialCalleeDefault. A slot before it
// that the partial leaves unmentioned takes its own accessor first, in
// ascending order, so each default runs once. ok is false when the callee
// is no std function with such accessors.
func (bl *irScalarBuilder) partialStdDefault(t *ast.Call, kinds []kind, slot int, filled []bool, openName []string, openKey []analysis.Pos) (func(*irScalarBuilder) (ir.Temp, bool), bool) {
	_, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || bl.g.std == nil {
		return nil, false
	}
	f := bl.stdShortCallee(t)
	if f == nil || !f.lowerable() {
		return nil, false
	}
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
		if !filled[s] && (!callDefaultFill(f, s) || canon.irSlot[s] == nil) {
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
			case filled[s]:
				v, k, _, ok = child.lower(child.g.madeRead(openName[s], openKey[s], t.Line, t.Col))
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
		return owner.irFileSlotSym(plan.sym, s)
	}
	return func(child *irScalarBuilder, args []ir.Temp) bool {
		return child.fillFromAccessors(t, kinds[:slot+1], args, nil, defaultHome{accessor: accessor}, len(args))
	}
}

// partialIfaceDefaults is the supplier body for open slot slot of a
// partial over interface function im (`&Shape.area(&1, &2)`), given the
// slots before it: the accessor a call leaving the slot out calls, the
// impl's for a receiver whose type is known (reachedImplHome) and one
// dispatched on it for an interface value (dispatchedCallHome). The
// receiver is a slot before this one, which the supplier holds. nil when
// it comes after the slot, so the supplier has no receiver.
func (bl *irScalarBuilder) partialIfaceDefaults(t *ast.Call, im *ast.InterfaceMethod, kinds []kind, slot int, calleeOrder []int) func(*irScalarBuilder, []ir.Temp) bool {
	recvAt := selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt()
	if recvAt < 0 || !slices.Contains(slotsBefore(calleeOrder, slot), recvAt) || len(kinds) != len(im.Params) {
		return nil
	}
	return func(child *irScalarBuilder, args []ir.Temp) bool {
		// The call's operands, which hold the receiver: args are those up
		// to the slot.
		operands := make([]ir.Temp, len(kinds))
		for i := range operands {
			operands[i] = ir.NoTemp
		}
		copy(operands, args)
		home, ok := child.dispatchedCallHome(im, kinds, operands)
		if !ok {
			home, _, ok = child.reachedImplHome(im, kinds, operands)
		}
		return ok && child.fillFromAccessors(t, kinds[:len(args)], args, nil, home, len(args))
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
	target := bl.g.calleeOf(t)
	if target.Kind == analysis.CalleeValue || bl.g.stdSiblingDecls[target.Decl] != nil {
		return nil
	}
	tpl := bl.g.irMonoTemplate(bl.g.sigByDecl[target.Decl])
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

// isLocalCallable reports whether id reads a function value this body or
// an enclosing one binds in a temporary, which a call reaches through the
// value: not a parameter of the function, whose callee the checker's
// record names.
func (bl *irScalarBuilder) isLocalCallable(id *ast.Ident) bool {
	l, held := bl.heldLocal(id)
	return held && l.temp != ir.NoTemp && l.k.tag == tagFunc
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
