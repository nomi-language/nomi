package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irLambdaPlan is what the builder keeps about the closure an `ir.FuncValue`
// builds, for a later call through a local alias (callablePlan): the
// parameters' names and kinds, the default suppliers and the signalling form.
type irLambdaPlan struct {
	// ctl is the signalling convention the enclosing Iter call widened this
	// callback to, or irCtlNone.
	ctl    irCtlForm
	source *ast.Lambda
	// names are the parameters' names a call writes, positionally: the
	// source's, or for a partial application's lambda the callee's
	// (partialSlots.names).
	names  []string
	body   *irScalarPlan
	params []kind
	group  *irLambdaGroup
	// entry, of kind entryKind, is the closure a call that leaves an
	// argument out goes through, when this is a partial with an open slot
	// whose default is computed and either written arguments or a computed
	// default it leaves unmentioned before that slot (partialApplication).
	// It takes this plan's parameters and then those held and hidden
	// slots, and entryGroup supplies every one a call omits: each written
	// argument once, then each callee default once, in the callee's order.
	// entryGroup is nil otherwise.
	entry      ir.Temp
	entryKind  kind
	entryGroup *irLambdaGroup
}

// A literal and its supplier closures share parameter kinds. Suppliers are
// FuncValues in the parent graph, referenced by their temporaries.
type irLambdaGroup struct {
	params    []kind
	suppliers []ir.Temp
	// order is the parameters in the order a call fills the ones it omits,
	// nil for their own order. A supplier takes the parameters before its
	// slot in this order (before). A partial capture's lambda takes its
	// `&N`s in written order but fills them in the callee's order, since a
	// callee's default reads the callee's earlier parameters:
	// `&span(b: &1, &2)` over `fn span(a: Int, b: Int = a * 2)` is `(b, a)`,
	// and b's supplier takes a.
	order []int
}

// fillOrder is the parameters in the order a call fills the ones it omits.
func (g *irLambdaGroup) fillOrder() []int {
	if g.order != nil {
		return g.order
	}
	order := make([]int, len(g.params))
	for i := range order {
		order[i] = i
	}
	return order
}

// before is the parameters slot i's supplier takes, in the order it takes
// them: the ones before i in fillOrder.
func (g *irLambdaGroup) before(i int) []int {
	order := g.fillOrder()
	for at, p := range order {
		if p == i {
			return order[:at]
		}
	}
	return nil
}

// supplierKind is slot i's supplier's function kind.
func (g *irLambdaGroup) supplierKind(gen *gen, i int) kind {
	return funcKindIn(gen, g.kindsAt(g.before(i)), g.params[i])
}

// kindsAt is the parameters' kinds at slots.
func (g *irLambdaGroup) kindsAt(slots []int) []kind {
	out := make([]kind, len(slots))
	for j, s := range slots {
		out[j] = g.params[s]
	}
	return out
}

// argsAt is args at slots, the arguments a supplier takes.
func argsAt(args []ir.Temp, slots []int) []ir.Temp {
	out := make([]ir.Temp, len(slots))
	for j, s := range slots {
		out[j] = args[s]
	}
	return out
}

func (bl *irScalarBuilder) lambda(t *ast.Lambda) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	params := make([]kind, len(t.Params))
	for i, p := range t.Params {
		if po, partial := bl.partialOpen[t]; partial {
			params[i] = po.kinds[i]
		} else {
			params[i] = bl.g.paramKind(p)
		}
		if !irCallableValueKind(params[i]) && params[i] != kindUnit {
			bl.g.dc.note("a lambda parameter outside the domain: " + params[i].nomi())
			return no()
		}
	}
	params = params[:bl.g.lambdaArity(t)]
	group := &irLambdaGroup{params: params, suppliers: make([]ir.Temp, len(params)), order: bl.partialOpen[t].order}
	for i := range group.suppliers {
		group.suppliers[i] = ir.NoTemp
	}
	for i, p := range t.Params[:len(params)] {
		if p.Default == nil {
			continue
		}
		supplier, fk, _, ok := bl.lambdaFunction(t, group.kindsAt(group.before(i)), p.Default, group, i)
		if !ok || funcResult(fk) != params[i] {
			bl.g.dc.note("a lambda parameter default outside the parameter's kind: " + params[i].nomi())
			return no()
		}
		group.suppliers[i] = supplier
	}
	return bl.lambdaFunction(t, params, t.Body, group, -1)
}

func (bl *irScalarBuilder) lambdaFunction(t *ast.Lambda, params []kind, expression ast.Node, group *irLambdaGroup, supplier int) (ir.Temp, kind, bool, bool) {
	// why is the stage the lambda stopped at, noted when nothing inside it
	// named a reason, so a decline says more than "a Lambda".
	var child *irScalarBuilder
	why := "a lambda body that is not a block ending in an expression"
	no := func() (ir.Temp, kind, bool, bool) {
		if bl.g.dc.why == "" {
			if child != nil && child.stalledStmt != nil {
				why = "a lambda statement that declined: " + constructName(child.stalledStmt)
			}
			// Reported at the lambda, as the enclosing `lower` would have
			// placed it; the reason names the stage.
			if line, col := nodePos(t); emittableLine(line) {
				bl.g.dc.line, bl.g.dc.col = line, col
			}
			bl.g.dc.note(why)
		}
		return ir.NoTemp, kindInvalid, false, false
	}
	var lead []ast.Node
	body := expression
	if supplier < 0 {
		// Types the body declares resolve by name while it lowers.
		defer bl.g.enterBlockTypes(t.Body)()
		lead, body = bl.g.irScalarBlock(t.Body)
	}
	if body == nil {
		return no()
	}
	if supplier < 0 && bl.g.unitFnLambda == t && analysis.EndsInDbg(body) {
		// A nested Unit `fn` ending in a `dbg` observation: the observation
		// runs as a statement and the function answers Unit.
		line, col := nodePos(body)
		lead = append(lead, body)
		body = &ast.TypeIdent{Name: "Unit", Line: line, Col: col}
	}
	at := bl.g.irNodePos(t)
	f := ir.NewFunc(at, "lambda")
	sh := &irFuncShell{fn: f, params: map[string]ir.Temp{}}
	sh.entry = f.NewBlock(at, "entry")
	child = &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl}
	child.root = t
	why = "a lambda parameter pattern"
	var tries []irPendingTry
	if supplier < 0 {
		// A try in the body leaves the lambda's own activation, the
		// innermost one.
		child.concTries, child.tryBoundary = &tries, "lambda"
		if bl.nestedFn != "" {
			child.tryBoundary = "fn " + bl.nestedFn
		}
	}
	if supplier < 0 {
		child.ctl = bl.ctlCallbacks[t]
	}
	// A supplier takes the parameters before its slot in the group's fill
	// order (irLambdaGroup.before).
	taken := make([]int, len(params))
	for i := range taken {
		taken[i] = i
	}
	if supplier >= 0 {
		copy(taken, group.before(supplier))
	}
	for i, s := range taken {
		p := t.Params[s]
		temp := bl.g.irAddParam(f, sh.paramSym(p), params[i])
		if i == 0 && child.ctl == irCtlReduce {
			child.ctlAcc = temp
		}
		if p.Destructure != nil {
			// `fn (a, b) ->`, `fn Point{x, y} ->`: the pattern's names, bound
			// from the argument as a binding with no `else` binds them.
			if !child.bindPattern(p.Destructure, temp, params[i]) {
				return no()
			}
			continue
		}
		if !ast.IsDiscardName(p.Name) {
			child.declareKey(bl.g.paramKey(t, s), temp, params[i])
			child.declareVar(p.Name, temp, params[i], p.Line, p.Col)
		}
	}
	if supplier < 0 {
		// A lambda passed where a function type with fewer parameters is
		// expected (lambdaArity) takes only those: each later parameter is
		// its default, evaluated on every call in parameter order, as a
		// call that leaves it out evaluates it.
		for j, p := range t.Params[len(params):] {
			k := bl.g.paramKind(p)
			v, vk, _, ok := child.lowerWant(p.Default, k)
			if !ok || vk != k {
				bl.g.dc.note("a lambda parameter default outside the parameter's kind: " + k.nomi())
				return no()
			}
			if !ast.IsDiscardName(p.Name) {
				child.declareKey(bl.g.paramKey(t, len(params)+j), v, k)
				child.declareVar(p.Name, v, k, p.Line, p.Col)
			}
		}
	}
	// A recursive nested fn refers to itself through its own closure: the
	// first capture parameter, which the FuncValue fills with the closure
	// it builds. A parameter of the same name shadows it.
	self := supplier < 0 && bl.recursive != nil && bl.recursive.lam == t
	if self {
		r := bl.recursive
		temp := bl.g.irAddParam(f, ir.NewSymbol(r.name), r.k)
		// The body's reads of the fn's name resolve to its declaration,
		// or an instance's, which in the body is this parameter.
		child.declareKey(r.key, temp, r.k)
	}
	// The lambda's result: a supplier's is its slot's parameter, a
	// reduction callback's its accumulator (through every branch, payload-free
	// variants such as None included), the solved one where the position
	// expects a wider or open result (lambdaWant), and otherwise the
	// checker's (lambdaResultKind). Every `return` in the body answers it.
	var sig irFuncSig
	resolved := supplier >= 0
	switch {
	case supplier >= 0:
		sig.result = group.params[supplier]
	case bl.foldCallbacks[t]:
		sig.result, resolved = params[0], true
	}
	if want, known := bl.g.lambdaWant, bl.g.lambdaWantFor == t; known && !resolved && child.ctl == irCtlNone {
		switch b := body.(type) {
		case *ast.If, *ast.Case:
			sig.result, resolved = want, true
		case *ast.ListLit:
			// `fn -> { io.print("left") [] }`: an empty literal answers the
			// solved result.
			if len(b.Items) == 0 && b.TypeName == nil {
				sig.result, resolved = want, true
			}
		case *ast.MapLit:
			if len(b.Entries) == 0 && b.TypeName == nil {
				sig.result, resolved = want, true
			}
		default:
			// `fn -> { io.print("empty") None }`.
			if _, _, _, bare := preludeValueName(body); bare {
				sig.result, resolved = want, true
			}
		}
		if want.tag == tagSeq || irHeldKind(want) || bl.g.declaredFnLambda == t {
			// `fn _ -> [20]` where an `(Int) -> Iter<Int>` is expected: the
			// body's list enters the declared sequence, as a named
			// function's result does. `fn n -> n + 1` where an
			// `(Int) -> Display` is expected, or a nested fn declared to
			// return `Display`: the body's value and every `return`'s
			// enter the existential.
			sig.result, resolved = want, true
		}
	}
	if !resolved {
		k, recorded := bl.lambdaResultKind(t)
		if !recorded {
			why = "a lambda the checker's records give no result"
			return no()
		}
		sig.result = k
	}
	if supplier < 0 {
		child.returnKind = sig.result
	}
	if supplier < 0 && child.ctl != irCtlNone {
		lead, body = bl.g.irCtlTailGuard(lead, body)
	}
	if block := t.Body; block != nil && supplier < 0 {
		// The lambda's own statements: a `with` there holds for the rest of
		// the lambda's activation, as a named function's does for its own.
		// A `defer` there runs when this invocation ends: the VM runs an
		// activation's pending deferred calls at each of its exits (a
		// normal end, a `return`, a callback's `break` or `continue`, a
		// `?` and a trap). A block statement in the body scopes its own.
		child.body = block
		child.deferOK = true
		child.openDeferScope(block)
		child.openWithScope(true)
	}
	if !child.leading(lead) {
		return no()
	}
	why = "a lambda's final expression: " + constructName(body)
	var answer ir.Temp
	var k kind
	var ok bool
	lam := t
	// A body ending in a block whose tail leaves: its statements, then its
	// tail here.
	body, restore, unwrapped := child.leavingBlocks(body)
	defer restore()
	if !unwrapped {
		return no()
	}
	child.markStmt(body, ir.StmtStatement)
	switch t := body.(type) {
	case *ast.Return:
		if supplier >= 0 {
			return no()
		}
		k, ok = child.explicitReturn(t)
	case *ast.Break, *ast.Continue:
		// `fn acc, x -> { break Some(x) }`: a signalling callback whose whole
		// body leaves it, as a `break` ending an arm does (armInto).
		if supplier >= 0 || child.ctl == irCtlNone {
			return no()
		}
		ok = child.ctlReturn(body)
		k = sig.result
	case *ast.If:
		sh.result = child.lambdaResultSlot(sig)
		k, ok = child.tailIf(t, sig)
	case *ast.Case:
		sh.result = child.lambdaResultSlot(sig)
		k, ok = child.tailCase(t, sig)
	default:
		if supplier < 0 && bl.g.neverFinishes(body) {
			// An `Iter.loop` with no `break` never answers: the lambda's
			// result is the one it has, which its returns answer.
			if _, ok = child.endlessLoop(body); !ok {
				return no()
			}
			k = sig.result
			break
		}
		if fill := bl.partialOpen[lam].defaults; supplier >= 0 && fill != nil && fill[supplier] != nil {
			// A partial's open slot whose callee default is not a literal:
			// the callee's default, evaluated in the callee's scope.
			answer, ok = fill[supplier](child)
			k = group.params[supplier]
		} else {
			answer, k, _, ok = child.lowerWant(body, sig.result)
		}
		if ok {
			child.b.SetTerm(ir.NewReturn(bl.g.irNodePos(body), answer))
		}
	}
	if ok && k == kindDiverged {
		// `return todo` as the whole body traps and returns nothing: the
		// lambda's result is the one it has.
		k = sig.result
	}
	if !ok {
		return no()
	}
	irHeldOperand(k, "a lambda's result")
	if supplier < 0 {
		if !child.settleConcurrentTries(tries, k) {
			bl.g.dc.note("a try whose operand does not exit to the lambda's result: " + k.nomi())
			return no()
		}
	}
	plan := &irScalarPlan{fn: f, result: k}
	bl.g.irRequireLint(f, "a lambda graph")
	var n *ir.FuncValue
	if self {
		n = ir.NewRecursiveFuncValue(at, bl.f.NewTemp(), f, child.captures...).KeepFields()
	} else {
		n = ir.NewFuncValue(at, bl.f.NewTemp(), f, child.captures...).KeepFields()
	}
	bl.b.Append(n)
	fk := funcKindIn(bl.g, params, k)
	names := lambdaParamNames(t, len(params))
	if po, partial := bl.partialOpen[t]; partial {
		names = po.names
	}
	bl.side(n.Dst(), irScalarSide{k: fk, lambda: &irLambdaPlan{ctl: child.ctl, source: t, names: names, body: plan, params: params, group: group}})
	return n.Dst(), fk, true, true
}

// lambdaResultSlot is the temporary a lambda's tail `if` or `case` writes.
// The lambda's result types it, so an arm whose value enters the result by
// erasure (a Pt where a Named is declared) is copied into the result
// type's storage, as a module function's result slot is, and an arm that
// produces the result type itself (a `todo`) agrees with it.
func (bl *irScalarBuilder) lambdaResultSlot(sig irFuncSig) ir.Temp {
	t := bl.f.NewTemp()
	bl.g.irTypeTemp(bl.f, t, sig.result)
	return t
}

// A captured parent binding has one parameter identity across all arm scopes.
type irLambdaCapture struct {
	temp ir.Temp
	k    kind
	// src is the enclosing builder's temporary the capture is supplied
	// from, by which capturedPlan finds the captured value's plan.
	src ir.Temp
	// name is the captured name, which names the capture's parameter.
	name string
}

// captureKey is what a closure captured: a local, by its key (readKey), or
// what no body binds, by its declaration, or, for an identifier the
// builder wrote that names no local, by its spelling.
type captureKey struct {
	local analysis.Pos
	decl  ast.Node
	name  string
}

// captureKeyOf is the captureKey of t, whose key readKey answered.
func (bl *irScalarBuilder) captureKeyOf(t *ast.Ident, key analysis.Pos, local bool) captureKey {
	if local {
		return captureKey{local: key}
	}
	if sym := bl.g.identRef(t); sym != nil && sym.Node != nil {
		return captureKey{decl: sym.Node}
	}
	return captureKey{name: t.Name}
}

// capture makes t's value a parameter of this closure, supplied by the
// value the enclosing builder holds for it where the closure is built: the
// local whose key is key, or what no body binds. A closure sees the value a
// name held when it was made; a later rebinding of the name is a new
// binding it never reads (spec §7 *Closures*).
func (bl *irScalarBuilder) capture(t *ast.Ident, key analysis.Pos, local bool) (ir.Temp, kind, bool, bool) {
	ck := bl.captureKeyOf(t, key, local)
	if held, ok := bl.captured[ck]; ok {
		if local {
			bl.declareKey(key, held.temp, held.k)
		}
		return held.temp, held.k, true, true
	}
	src, k, _, ok := bl.parent.lower(t)
	if ok && k == kindUnit {
		// A Unit holds nothing to carry: the closure makes its own, here,
		// at each use, so no use reads a value another branch made.
		u := ir.NewUnit(bl.g.irNodePos(t), bl.f.NewTemp())
		bl.b.Append(u)
		return u.Dst(), kindUnit, true, true
	}
	if !ok || !irCallableValueKind(k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	dst := bl.g.irAddParam(bl.f, ir.NewSymbol(t.Name), k)
	bl.captures = append(bl.captures, src)
	if bl.captured == nil {
		bl.captured = map[captureKey]irLambdaCapture{}
	}
	bl.captured[ck] = irLambdaCapture{temp: dst, k: k, src: src, name: t.Name}
	if local {
		bl.declareKey(key, dst, k)
	}
	bl.declareCapture(t.Name, dst, k, bl.capturedDecl(t.Name, src))
	return dst, k, true, true
}

// callablePlan follows only plain local aliases and captures. Callable
// parameters and branch-selected or returned values carry no statically
// recoverable defaults.
func (bl *irScalarBuilder) callablePlan(temp ir.Temp) *irLambdaPlan {
	for {
		switch n := bl.f.Def(temp).(type) {
		case *ir.Bind:
			temp = n.Src()
		case *ir.Copy:
			if int(n.Dst()) >= len(bl.sides) || bl.sides[n.Dst()].copy == irCopyNone {
				return nil
			}
			temp = n.Src()
		case *ir.FuncValue:
			return bl.sides[n.Dst()].lambda
		case nil:
			return bl.capturedPlan(temp)
		default:
			return nil
		}
	}
}

// capturedPlan is the plan of a lambda this closure captured, as the
// enclosing builder holds it: `g = fn -> f(1)` calls the f its parent built.
// The plan's names carry over. Its default suppliers are closures of the
// parent's graph, so each is captured too, beside the value, and the copy's
// suppliers are those captures. nil when temp is no capture, or the parent
// has no plan for the value.
func (bl *irScalarBuilder) capturedPlan(temp ir.Temp) *irLambdaPlan {
	if bl.parent == nil {
		return nil
	}
	for _, held := range bl.captured {
		name := held.name
		if held.temp != temp {
			continue
		}
		if plan, done := bl.capturedPlans[temp]; done {
			return plan
		}
		outer := bl.parent.callablePlan(held.src)
		if outer == nil {
			return nil
		}
		plan := *outer
		if outer.group != nil {
			plan.group = bl.captureGroup(outer.group, name+"\x00default")
		}
		if outer.entryGroup != nil {
			// A partial's entry and its suppliers are closures of the
			// parent's graph too.
			plan.entry = bl.g.irAddParam(bl.f, ir.NewSymbol(name+"\x00entry"), outer.entryKind)
			bl.captures = append(bl.captures, outer.entry)
			plan.entryGroup = bl.captureGroup(outer.entryGroup, name+"\x00entrydefault")
			// Held by a name no program spells, so a partial over the
			// entry (partialOverEntry) in a closure nested here finds the
			// entry's own plan through this capture.
			bl.captured[captureKey{local: bl.g.madeLocal()}] = irLambdaCapture{temp: plan.entry, k: outer.entryKind, src: outer.entry, name: name + "\x00entry"}
		}
		if bl.capturedPlans == nil {
			bl.capturedPlans = map[ir.Temp]*irLambdaPlan{}
		}
		bl.capturedPlans[temp] = &plan
		return &plan
	}
	return nil
}

// captureGroup captures each of outer's suppliers, closures of the parent's
// graph, as parameters of this closure named from prefix, and answers the
// group whose suppliers are those captures.
func (bl *irScalarBuilder) captureGroup(outer *irLambdaGroup, prefix string) *irLambdaGroup {
	group := &irLambdaGroup{params: outer.params, suppliers: make([]ir.Temp, len(outer.suppliers)), order: outer.order}
	for i, s := range outer.suppliers {
		group.suppliers[i] = ir.NoTemp
		if s == ir.NoTemp {
			continue
		}
		// A supplier takes the parameters before its slot in the group's
		// fill order and answers the slot's value.
		sk := outer.supplierKind(bl.g, i)
		dst := bl.g.irAddParam(bl.f, ir.NewSymbol(prefix+strconv.Itoa(i)), sk)
		bl.captures = append(bl.captures, s)
		group.suppliers[i] = dst
	}
	return group
}

func (bl *irScalarBuilder) indirectCall(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	callee, fk, _, ok := bl.lower(t.Func)
	if ok && irCallableDistinct(fk) {
		// `handler(input)` over `type Callback (String) -> String`: the call
		// goes through the function the distinct wraps.
		callee = bl.distinctProjection(t.Func, callee, fk, false, "irlambda.go indirectCall")
		fk = fk.def.inner
	}
	if !ok || fk.tag != tagFunc {
		return no()
	}
	params := funcParams(fk)
	metadata := bl.callablePlan(callee)
	plan, planned := bl.g.callPlan(t, len(params))
	if !planned {
		panic("irbuild: a call through a function value placed for other parameters than the value's")
	}
	args := make([]ir.Temp, len(params))
	for i := range args {
		args[i] = ir.NoTemp
	}
	defaulted := len(plan.defaulted) > 0
	if defaulted && metadata == nil {
		// A local copy of a declared function (`g = parse`) keeps its
		// defaults: the call goes through the function's shorter value,
		// whose body calls it by name with the arguments written here.
		// The checker records the function the copy was made from: a
		// function value with no declaration behind it has no defaults,
		// and leaving out its arguments is a checker error.
		rec := bl.g.callDefaultsOf(t)
		if rec == nil || rec.Ref == nil {
			panic("irbuild: a short call of a local function value with no CallDefaults record naming its function")
		}
		short, sk, _, ok := bl.callFuncValueOf(rec.Ref, funcRefName(rec.Ref), params[:len(t.Args)], funcResult(fk), rec)
		if !ok {
			return no()
		}
		callee, fk, params, defaulted = short, sk, funcParams(sk), false
		args = args[:len(params)]
	}
	result := funcResult(fk)
	if !irCallableValueKind(result) && result != kindUnit {
		return no()
	}
	for _, i := range plan.order {
		a, slot := argExpr(t.Args[i]), plan.slots[i]
		v, k, _, ok := bl.lowerTypedOperand(a, params[slot])
		if !ok {
			return no()
		}
		// Only the final operand may stay unforced. An assertion subject
		// also forces it, because it names the final operand twice, in its
		// row and in the call.
		v, k, ok = bl.convertTo(a, v, k, params[slot])
		if !ok {
			return no()
		}
		args[slot] = v
	}
	var order []int
	var group *irLambdaGroup
	if defaulted {
		group = metadata.group
		if metadata.entryGroup != nil {
			// A partial whose omitted defaults come after hidden ones: the
			// call goes through its entry, which takes the hidden slots
			// too, so each default is evaluated here, once.
			group, callee = metadata.entryGroup, metadata.entry
			params = group.params
			for range params[len(args):] {
				args = append(args, ir.NoTemp)
			}
		}
		order = group.fillOrder()
	}
	for _, i := range order {
		if args[i] != ir.NoTemp {
			continue
		}
		supplier := group.suppliers[i]
		if supplier == ir.NoTemp {
			return no()
		}
		call := ir.NewIndirectCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), supplier, argsAt(args, group.before(i))...)
		bl.b.Append(call)
		bl.side(call.Dst(), irScalarSide{k: params[i], deferrable: true})
		args[i] = call.Dst()
		for j := i + 1; j < len(args); j++ {
			if args[j] == ir.NoTemp {
				break
			}
		}
	}
	// A call through a value shows its arguments exactly as a direct call
	// does, after any defaults are filled; a piped call shows none.
	if t != bl.pipedCall && bl.recording > 0 {
		written := make([]ir.Temp, len(t.Args))
		kinds := make([]kind, len(t.Args))
		for i := range t.Args {
			written[i], kinds[i] = args[plan.slots[i]], params[plan.slots[i]]
		}
		bl.recordCallArgs(t.Args, written, kinds, result)
	}
	n := ir.NewIndirectCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), callee, args...)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result, deferrable: true})
	return n.Dst(), result, false, true
}

// irCallableValueKind extends the retained value domain with function signatures
// whose own inputs (Unit among them) and output are representable, including
// scalar-leaf lists, and with existentials: the VM holds an existential as the
// concrete value, so it crosses a call, a lambda and a result as that value
// does.
func irCallableValueKind(k kind) bool {
	if irRetainedValueKind(k) || irRetainedSeqKind(k) || irHeldKind(k) {
		return true
	}
	if k.tag != tagFunc || k.comp == nil || len(k.comp.parts) == 0 {
		return false
	}
	for _, p := range funcParams(k) {
		if p != kindUnit && !irCallableValueKind(p) {
			return false
		}
	}
	result := funcResult(k)
	return result == kindUnit || irCallableValueKind(result)
}

// irRetainedSeqKind is a lowered `Iter<T>` over a retained element: a value
// like any other, held as the `rt.Seq` closure, in a binding, a parameter, a
// result, a field, a payload or an element. A declared `Iter<T>` has this
// kind too (typeOf), and a source entering one is viewed (seqView).
func irRetainedSeqKind(k kind) bool {
	return k.tag == tagSeq && k.comp != nil && len(k.comp.parts) == 1 && (irCallableValueKind(k.comp.parts[0]) || k.comp.parts[0] == kindUnit)
}

// funcRef retains module-function values. A function with defaults is its
// full parameter list as a value, except where the checker typed it shorter,
// which callFuncValue builds (defaultFilledRef). A generic one is
// the instance the checker instantiated the reference at (funcref.go). The
// symbol is shared with direct calls and bodies.
func (bl *irScalarBuilder) funcRef(t *ast.Ident) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if sym := bl.g.checkedRef(t.Line, t.Col); sym != nil && sym.Kind == analysis.SymbolOnce {
		return no()
	}
	decl := bl.g.funcDeclAt(t.Line, t.Col)
	if v, k, mobile, ok, handled := bl.stdOwnFuncRef(t, decl, true); handled {
		// A stdlib sibling resolves before `g.funcs`, as a call to it does.
		return v, k, mobile, ok
	}
	if bl.g.defaultFilledRef(t) {
		return bl.callFuncValue(t, t.Name)
	}
	sig := bl.g.sigByDecl[decl]
	if sig == nil && bl.g.files != nil {
		// `ident` after `import span.{ident}`.
		if site, ok := bl.g.files.byDecl[decl]; ok && decl != nil && site.fn != nil && site.unit != bl.g.fileUnit {
			return bl.siblingFuncRef(t, site.unit, site.fn, site.host)
		}
	}
	if sig == nil {
		// `print` after `import std::io.print`, `read_line` after
		// `import std::io.read_line`.
		if v, k, mobile, ok, handled := bl.stdBareFuncRef(t); handled {
			return v, k, mobile, ok
		}
	}
	if tpl := bl.g.irMonoTemplate(sig); tpl != nil {
		inst, ok := bl.g.monoInstanceAt(t, tpl, t.Name)
		if !ok {
			return no()
		}
		sig = inst.sig
	}
	if sig == nil || !sig.lowerable || sig.decl == nil || sig.dict != nil || sig.tps != nil {
		if v, k, mobile, ok, handled := bl.stdOwnFuncRef(t, decl, false); handled {
			return v, k, mobile, ok
		}
		return no()
	}
	fk := funcKindIn(bl.g, sig.params, sig.result)
	if !irCallableValueKind(fk) {
		return no()
	}
	ctl := bl.g.namedCtlForm(sig.decl)
	if ctl != irCtlNone && bl.ctlRefs[t] != ctl {
		bl.g.dc.note("an iter-sensitive function passed where its break or continue cannot be caught: " + t.Name)
		return no()
	}
	ref := ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irFunctionCallee(sig)).KeepFields()
	bl.b.Append(ref)
	bl.side(ref.Dst(), irScalarSide{k: fk, ctl: ctl})
	return ref.Dst(), fk, true, true
}

// lambdaResultKind is the result kind of the function type the checker's
// records give lam: its own (recordedKind), or where its own result is
// Infallible (a body that only leaves, `fn x -> { break Some(x) }`) the
// result of the function type its position settled (settledKind:
// `(T) -> Bool` for Iter.filter's callback). A lambda the builder writes
// carries the type of the source it stands for (carryType). ok is false
// where lam has no record that names a result, and the lambda declines
// (noteUnrecordedKind counts it).
func (bl *irScalarBuilder) lambdaResultKind(lam *ast.Lambda) (kind, bool) {
	result := func(k kind) kind {
		if k.tag != tagFunc || k.comp == nil || len(k.comp.parts) == 0 {
			return kindInvalid
		}
		return funcResult(k)
	}
	k := kindInvalid
	if own, recorded := bl.g.recordedKind(lam); recorded {
		k = result(own)
	}
	if settled, isSettled := bl.g.settledKind(lam); k == kindNever && isSettled {
		k = result(settled)
	}
	// kindInvalid: lookup — a record no result kind represents is counted as no record.
	if k == kindInvalid || k == kindNever {
		bl.noteUnrecordedKind("lambda result", lam)
		return kind{}, false
	}
	return k, true
}

// lambdaArity is the number of parameters lambda t takes as a value: all of
// them, unless the checker typed it where a function type with fewer
// parameters is expected and every parameter past those has a default
// (`Iter.map(xs, fn x, y = 10 -> x + y)`, analysis.checkLambdaExpecting).
// A pattern parameter past them keeps the lambda whole.
func (g *gen) lambdaArity(t *ast.Lambda) int {
	if g.fa == nil {
		return len(t.Params)
	}
	ft, _ := analysis.ResolveClosed(g.srcFA().ExprTypes[t]).(*analysis.FuncType)
	if ft == nil || len(ft.Params) >= len(t.Params) {
		return len(t.Params)
	}
	for _, p := range t.Params[len(ft.Params):] {
		if p.Default == nil || p.Destructure != nil {
			return len(t.Params)
		}
	}
	return len(ft.Params)
}
