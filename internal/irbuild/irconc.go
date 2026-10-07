package irbuild

// `concurrent { body }` and the `std/tasks` calls inside it.
//
// The block lowers as a zero-parameter function value whose body is the
// block's statements, passed to the `concurrent` host crossing. The body is a
// separate `ir.Func`, so a `try` or `return` in it ends the block, as the
// checker's boundary says. The VM's adapter opens an `rt` scope on the caller's
// runtime frame, runs the body on it, and exits the scope on every path. Go
// reads the pair back as an immediately invoked func literal whose scope exit
// is `defer rt.ScopeExit(fr)`.
//
// `Task.spawn(|| body)` takes a zero-parameter lambda literal;
// `Task.spawn_all` takes a sequence, a one-parameter function and its bound;
// `Task.await`, `Task.outcome` and `Task.cancel` take one task handle and
// `Task.await_all` a list of them. Each is a host crossing named
// `Task.<method>`, spelled with an `rt` target, and the VM calls the
// `rt` task runtime.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// concurrent lowers `concurrent { body }` in expression position.
func (bl *irScalarBuilder) concurrent(t *ast.ConcurrentBlock) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.recording > 0 || t.Body == nil {
		return no()
	}
	defer bl.g.enterBlockTypes(t.Body)()
	lead, body := bl.g.irScalarBlock(t.Body, "")
	if body == nil {
		return no()
	}
	at := bl.g.irNodePos(t)
	f := ir.NewFunc(at, "concurrent")
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	var tries []irPendingTry
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{},
		boundK: map[string]kind{}, inferReturn: &resultInference{}, concTries: &tries, tryBoundary: "concurrent"}
	child.openWithScope(true)
	if !child.leading(lead) {
		return no()
	}
	sig := irFuncSig{inferResult: true}
	var k kind
	var ok bool
	switch t := body.(type) {
	case *ast.Return:
		k, ok = child.explicitReturn(t)
	case *ast.If:
		sh.result = f.NewTemp()
		k, ok = child.tailIf(t, sig)
	case *ast.Case:
		sh.result = f.NewTemp()
		k, ok = child.tailCase(t, sig)
	default:
		var answer ir.Temp
		answer, k, _, ok = child.lower(body)
		if ok {
			child.b.SetTerm(ir.NewReturn(bl.g.irNodePos(body), answer))
		}
	}
	if ok && k == kindDiverged {
		// Every arm of the tail region returned: the body's result is what
		// those returns settled.
		k = child.returnKind
	}
	if !ok || (!irCallableValueKind(k) && k != kindUnit) {
		return no()
	}
	if _, agree := child.inferReturn.settle(k); !agree || !child.settleConcurrentTries(tries, k) {
		return no()
	}
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a `concurrent` body graph that does not lint: " + err.Error())
		return no()
	}
	fv := ir.NewFuncValue(at, bl.f.NewTemp(), f, child.captures...)
	bl.b.Append(fv)
	bl.side(fv.Dst(), irScalarSide{k: funcKindIn(bl.g, nil, k)})
	call := ir.NewHostCall(at, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym("concurrent", "concurrent"), fv.Dst())
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: k, deferrable: true})
	return call.Dst(), k, true, true
}

// taskCall lowers the `std/tasks` calls this producer retains. Its owner
// resolved to std's `Task` declaration (stdIntrinsicOwner), so a user `Task`
// never reaches here.
func (bl *irScalarBuilder) taskCall(t *ast.Call, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if method == "spawn_all" {
		return bl.taskSpawnAll(t)
	}
	if namedArgNode(t.Args) != nil || len(t.Args) != 1 {
		return no()
	}
	if method == "spawn" {
		lam, isLambda := t.Args[0].(*ast.Lambda)
		if !isLambda || len(lam.Params) != 0 {
			return no()
		}
		fn, fk, _, ok := bl.lower(lam)
		if !ok {
			return no()
		}
		payload := funcResult(fk)
		handle, built := bl.g.taskHandleKind(payload)
		if !built {
			return no()
		}
		return bl.concHostEmit(t, "Task.spawn", handle, fn)
	}
	fn, known := taskFuncs[method]
	if !known || fn.spawn {
		return no()
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	handle := args.kinds[0]
	if method == "await_all" {
		if handle.tag != tagList || handle.comp == nil {
			return no()
		}
		handle = handle.comp.parts[0]
	}
	payload, isTask := taskElem(handle)
	if !isTask {
		return no()
	}
	var result kind
	switch method {
	case "await_all":
		result = bl.g.listKind(payload)
		// kindInvalid: lookup — a List over a payload with no interned kind declines.
		if result == kindInvalid {
			return no()
		}
	case "await":
		result = payload
	case "cancel":
		result = kindUnit
	case "outcome":
		result = bl.g.preludeInstanceOf(preludeSpecNamed("Outcome"), []kind{payload})
		if !irRetainedValueKind(result) {
			return no()
		}
	}
	return bl.concHostEmit(t, "Task."+method, result, args.temps[0])
}

// taskSpawnAll lowers `Task.spawn_all(source, f, max_running: n)` over a
// sequence source. Arguments are placed by taskPlacement and evaluated in
// its order, forcing every one but the last evaluated.
func (bl *irScalarBuilder) taskSpawnAll(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	plan, planned := taskPlacement(taskFuncs["spawn_all"], t.Args)
	if !planned || len(t.Args) != 3 {
		return no()
	}
	if _, contiguous := contiguousSlots(plan); !contiguous {
		return no()
	}
	temps := make([]ir.Temp, len(t.Args))
	kinds := make([]kind, len(t.Args))
	for _, i := range plan.order {
		a := argExpr(t.Args[i])
		v, k, _, ok := bl.lower(a)
		if !ok {
			return no()
		}
		temps[plan.slots[i]], kinds[plan.slots[i]] = v, k
	}
	src, fn := kinds[0], kinds[1]
	if src.tag == tagList {
		// A List source is viewed as the sequence rt takes, as Iter's
		// adapters view one.
		view, elem, ok := bl.iterPlainView(argExpr(t.Args[0]), temps[0], src)
		if !ok {
			return no()
		}
		temps[0], src = view, seqKindIn(bl.g, elem)
	}
	if src.tag != tagSeq || src.comp == nil || kinds[2] != kindInt {
		return no()
	}
	elem := seqElem(src)
	if fn.tag != tagFunc || fn.comp == nil || len(funcParams(fn)) != 1 || funcParams(fn)[0] != elem {
		return no()
	}
	payload := funcResult(fn)
	handle, built := bl.g.taskHandleKind(payload)
	if !built {
		return no()
	}
	result := bl.g.listKind(handle)
	// kindInvalid: lookup — a List over a handle with no interned kind declines.
	if result == kindInvalid {
		return no()
	}
	return bl.concHostEmit(t, "Task.spawn_all", result, temps...)
}

// concHostEmit appends one task or channel crossing and records its Go target.
func (bl *irScalarBuilder) concHostEmit(t *ast.Call, name string, result kind, args ...ir.Temp) (ir.Temp, kind, bool, bool) {
	c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(name, name), args...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: result, deferrable: true})
	return c.Dst(), result, false, true
}
