package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irLambdaPlan holds Go spelling facts beside the executable closure body.
// Bodies contain fresh bindings and effects followed by an expression, with
// plain parameters and stable captures.
// Enclosing functions combine stable captures with no rebinding; lambdas in
// test bodies and destructuring lambdas are declined.
type irLambdaPlan struct {
	// ctl is the signalling convention the enclosing Iter call widened this
	// callback to, or irCtlNone.
	ctl      irCtlForm
	source   *ast.Lambda
	body     *irScalarPlan
	params   []kind
	group    *irLambdaGroup
	supplier int // -1 for the lambda itself; otherwise the supplied parameter slot
}

// A literal and its supplier closures share parameter spelling. Suppliers are
// executable FuncValues in the parent graph, referenced by their temporaries.
type irLambdaGroup struct {
	params    []kind
	suppliers []ir.Temp
}

func (bl *irScalarBuilder) lambda(t *ast.Lambda) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	params := make([]kind, len(t.Params))
	for i, p := range t.Params {
		if p.Destructure != nil && (p.Default != nil && !(i == 0 && bl.reduceSeeds[t])) {
			return no()
		}
		_, tuple := p.Destructure.(*ast.TuplePattern)
		if pk, partial := bl.partialKinds[t]; partial {
			params[i] = pk[i]
		} else if p.TypeAnnotation != nil {
			params[i] = bl.g.typeOf(p.TypeAnnotation)
		} else if p.Destructure != nil && !tuple {
			// A self-typing pattern names its type: `|Dur(n)|` takes a Dur,
			// `|Point{x, y}|` a Point and `|Box.Extent{w, h}|` a Box.
			params[i] = bl.g.irPatternHeadKind(p.Destructure)
			// kindInvalid: lookup — the head names no distinct; a struct or enum head may name the type, and a miss declines below.
			if params[i] == kindInvalid {
				params[i] = bl.g.irParamPatternKind(p)
			}
		} else if tuple {
			// `|(k, v)|`, `|(_, v)|`, `|(a, (b, _))|`: the type the checker
			// recorded for the whole pattern; a miss declines below.
			params[i] = bl.g.irParamPatternKind(p)
		} else {
			_, params[i] = bl.g.inferredParamKind(p)
		}
		if !irCallableValueKind(params[i]) && params[i] != kindUnit {
			irDeclineNote("a lambda parameter outside the domain: " + params[i].nomi())
			return no()
		}
	}
	group := &irLambdaGroup{params: params, suppliers: make([]ir.Temp, len(params))}
	for i := range group.suppliers {
		group.suppliers[i] = ir.NoTemp
	}
	for i, p := range t.Params {
		if p.Default == nil {
			continue
		}
		if i == 0 && bl.reduceSeeds[t] {
			continue
		}
		supplier, fk, _, ok := bl.lambdaFunction(t, params[:i], p.Default, group, i)
		if !ok || funcResult(fk) != params[i] {
			return no()
		}
		group.suppliers[i] = supplier
	}
	return bl.lambdaFunction(t, params, t.Body, group, -1)
}

func (bl *irScalarBuilder) lambdaFunction(t *ast.Lambda, params []kind, expression ast.Node, group *irLambdaGroup, supplier int) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	var lead []ast.Node
	body := expression
	if supplier < 0 {
		// Types the body declares resolve by name while it lowers.
		defer bl.g.enterBlockTypes(t.Body)()
		lead, body = bl.g.irScalarBlock(t.Body, "")
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
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	var tries []irPendingTry
	if supplier < 0 {
		child.inferReturn = &resultInference{}
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
	var patterned []irPatternParam
	for i, p := range t.Params[:len(params)] {
		temp := bl.g.irAddParam(f, sh.paramSym(p), params[i])
		if i == 0 && child.ctl == irCtlReduce {
			child.ctlAcc = temp
		}
		if irPrologueParam(p, params[i]) {
			patterned = append(patterned, irPatternParam{pat: p.Destructure, temp: temp, k: params[i]})
			continue
		}
		if tp, tuple := p.Destructure.(*ast.TuplePattern); tuple {
			for j, c := range tp.Patterns {
				id, named := c.(*ast.IdentPattern)
				if !named || ast.IsDiscardName(id.Name) {
					continue
				}
				part := params[i].comp.parts[j]
				read := child.tupleProjection(tp, temp, j, part)
				child.patternBinding(id, id.Name, read, part)
			}
			continue
		}
		if p.Destructure != nil {
			// `|Dur(n)|`: the argument's inner value, bound to n, as a
			// distinct destructure statement binds it.
			name, _ := irDistinctParamBinding(p.Destructure, params[i])
			if !ast.IsDiscardName(name) {
				inner := child.distinctProjection(p.Destructure, temp, params[i], true, "irlambda.go distinct parameter")
				child.patternBinding(p.Destructure, name, inner, params[i].def.inner)
			}
			continue
		}
		if !ast.IsDiscardName(p.Name) {
			child.bound[p.Name], child.boundK[p.Name] = temp, params[i]
		}
	}
	// A recursive nested fn refers to itself through its own closure: the
	// first capture parameter, which the FuncValue fills with the closure
	// it builds. A parameter of the same name shadows it.
	self := supplier < 0 && bl.recursive != nil && bl.recursive.lam == t
	if self {
		r := bl.recursive
		temp := bl.g.irAddParam(f, sh.localSym(r.name), r.k)
		if _, shadowed := child.bound[r.name]; !shadowed {
			child.bound[r.name], child.boundK[r.name] = temp, r.k
		}
	}
	if len(patterned) != 0 && !child.patternParams(patterned) {
		return no()
	}
	sig := irFuncSig{inferResult: true}
	// A reduction callback returns its accumulator type. Keep that context
	// through every branch, including payload-free variants such as None.
	if supplier < 0 && bl.reduceSeeds[t] {
		sig = irFuncSig{result: params[0]}
		child.returnKind = params[0]
	}
	if want, known := bl.g.lambdaWant, bl.g.lambdaWantFor == t; known && supplier < 0 && sig.inferResult && child.ctl == irCtlNone {
		switch b := body.(type) {
		case *ast.If, *ast.Case:
			sig = irFuncSig{result: want}
		case *ast.ListLit:
			// `|| { io.print("left") [] }`: an untyped literal answers the
			// solved result.
			if len(b.Items) == 0 && b.TypeName == nil {
				sig = irFuncSig{result: want}
			}
		case *ast.MapLit:
			if len(b.Entries) == 0 && b.TypeName == nil {
				sig = irFuncSig{result: want}
			}
		default:
			// `|| { io.print("empty") None }`.
			if _, _, _, bare := preludeValueName(body); bare {
				sig = irFuncSig{result: want}
			}
		}
		if want.tag == tagSeq {
			// `|_| [20]` where an `(Int) -> Iter<Int>` is expected: the
			// body's list enters the declared sequence, as a named
			// function's result does.
			sig = irFuncSig{result: want}
			child.returnKind = want
		}
	}
	if supplier < 0 && child.ctl != irCtlNone {
		lead, body = bl.g.irCtlTailGuard(lead, body)
		if cond, isIf := body.(*ast.If); isIf && cond.Else == nil && sig.inferResult {
			// `|s| if s == "STOP" { break }`: an `if` with no `else` is Unit
			// either way, so a signalling arm has a known result to leave
			// with.
			sig = irFuncSig{result: kindUnit}
		}
	}
	if block := t.Body; block != nil && supplier < 0 {
		// The lambda's own statements: a `with` there holds for the rest of
		// the lambda's activation, as a named function's does for its own.
		// A defer is still declined.
		child.body = block
		child.deferScope = &irDeferScope{block: block}
		child.openWithScope(true)
	}
	if !child.leading(lead) {
		return no()
	}
	var answer ir.Temp
	var k kind
	var ok bool
	switch t := body.(type) {
	case *ast.Return:
		if supplier >= 0 {
			return no()
		}
		k, ok = child.explicitReturn(t)
	case *ast.Break, *ast.Continue:
		// `|acc, x| { break Some(x) }`: a signalling callback whose whole
		// body leaves it, as a `break` ending an arm does (armInto).
		if supplier >= 0 || child.ctl == irCtlNone || sig.inferResult {
			return no()
		}
		k, ok = sig.result, child.ctlReturn(body)
	case *ast.If:
		sh.result = f.NewTemp()
		k, ok = child.tailIf(t, sig)
	case *ast.Case:
		sh.result = f.NewTemp()
		k, ok = child.tailCase(t, sig)
	default:
		if supplier >= 0 {
			answer, k, _, ok = child.lowerWant(body, group.params[supplier])
		} else if !sig.inferResult {
			answer, k, _, ok = child.lowerWant(body, sig.result)
		} else {
			answer, k, _, ok = child.lower(body)
		}
		if ok {
			child.b.SetTerm(ir.NewReturn(bl.g.irNodePos(body), answer))
		}
	}
	if ok && k == kindDiverged {
		// Every arm of the tail region returned: the lambda's result is
		// what those returns settled.
		k = child.returnKind
	}
	if !ok || (!irCallableValueKind(k) && k != kindUnit) {
		if ok {
			irDeclineNote("a lambda result outside the domain: " + k.nomi())
		}
		return no()
	}
	if child.inferReturn != nil {
		if _, agree := child.inferReturn.settle(k); !agree {
			irDeclineNote("a lambda whose returns disagree")
			return no()
		}
		if !child.settleConcurrentTries(tries, k) {
			irDeclineNote("a try whose operand does not exit to the lambda's result: " + k.nomi())
			return no()
		}
	}
	plan := &irScalarPlan{fn: f, result: k}
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a lambda graph that does not lint: " + err.Error())
		return no()
	}
	var n *ir.FuncValue
	if self {
		n = ir.NewRecursiveFuncValue(at, bl.f.NewTemp(), f, child.captures...)
	} else {
		n = ir.NewFuncValue(at, bl.f.NewTemp(), f, child.captures...)
	}
	bl.b.Append(n)
	fk := funcKindIn(bl.g, params, k)
	bl.side(n.Dst(), irScalarSide{k: fk, lambda: &irLambdaPlan{ctl: child.ctl, source: t, body: plan, params: params, group: group, supplier: supplier}})
	return n.Dst(), fk, true, true
}

// A captured parent binding has one parameter identity across all arm scopes.
type irLambdaCapture struct {
	temp ir.Temp
	k    kind
}

// capture makes name a parameter of this closure, supplied by the value the
// enclosing builder holds for it where the closure is built. A closure sees
// the value a name held when it was made; a later rebinding of the name is a
// new binding it never reads (spec §23).
func (bl *irScalarBuilder) capture(t *ast.Ident) (ir.Temp, kind, bool, bool) {
	if held, ok := bl.captured[t.Name]; ok {
		bl.bound[t.Name], bl.boundK[t.Name] = held.temp, held.k
		return held.temp, held.k, true, true
	}
	src, k, _, ok := bl.parent.lower(t)
	if !ok || !irCallableValueKind(k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	dst := bl.g.irAddParam(bl.f, bl.sh.localSym(t.Name), k)
	bl.captures = append(bl.captures, src)
	if bl.captured == nil {
		bl.captured = map[string]irLambdaCapture{}
	}
	bl.captured[t.Name] = irLambdaCapture{temp: dst, k: k}
	bl.bound[t.Name], bl.boundK[t.Name] = dst, k
	return dst, k, true, true
}

// callablePlan follows only plain local aliases. Callable parameters and
// branch-selected or returned values carry no statically recoverable defaults.
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
		default:
			return nil
		}
	}
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
	var names []string
	if metadata != nil {
		names = lambdaParamNames(metadata.source, len(params))
	}
	plan, planned := argSlotPlan(t.Args, params, names)
	if !planned {
		return no()
	}
	args := make([]ir.Temp, len(params))
	for i := range args {
		args[i] = ir.NoTemp
	}
	defaulted := len(t.Args) < len(params)
	if defaulted && metadata == nil {
		return no()
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
		v, k, ok = bl.coerceEmpty(a, v, k, params[slot])
		if !ok {
			return no()
		}
		args[slot] = v
	}
	for i := range args {
		if args[i] != ir.NoTemp {
			continue
		}
		supplier := metadata.group.suppliers[i]
		if supplier == ir.NoTemp {
			return no()
		}
		call := ir.NewIndirectCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), supplier, args[:i]...)
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
// does, and with std's payload-free markers (`ChannelClosed`).
func irCallableValueKind(k kind) bool {
	if irRetainedValueKind(k) || irRetainedSeqKind(k) || irExistentialKind(k) || irStdMarkerKind(k) {
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
// kind too (structuralTypeOf), and a source entering one is viewed (seqView).
func irRetainedSeqKind(k kind) bool {
	return k.tag == tagSeq && k.comp != nil && len(k.comp.parts) == 1 && (irCallableValueKind(k.comp.parts[0]) || k.comp.parts[0] == kindUnit)
}

// funcRef retains module-function values with no defaults. A generic one is
// the instance the checker instantiated the reference at (funcref.go). The
// symbol is shared with direct calls and bodies.
func (bl *irScalarBuilder) funcRef(t *ast.Ident) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.g.onces[t.Name] != nil {
		return no()
	}
	sig := bl.g.funcs[t.Name]
	if sig == nil && bl.g.files != nil {
		// `ident` after `import span.{ident}`.
		if site, ok := bl.g.files.lookupBare(bl.g.fa, t); ok && site.fn != nil && site.unit != bl.g.fileUnit {
			return bl.siblingFuncRef(t, site.unit, site.fn, site.host)
		}
	}
	if sig == nil {
		// `print` after `import std/io.print`, `read_file` after
		// `import std/io.read_file`.
		if v, k, mobile, ok, handled := bl.stdBareFuncRef(t); handled {
			return v, k, mobile, ok
		}
	}
	if tpl := bl.g.irMonoTemplate(sig); tpl != nil {
		inst, ok := bl.g.funcRefInstance(t, tpl, t.Name)
		if !ok {
			return no()
		}
		sig = inst.sig
	}
	if sig == nil || !sig.lowerable || sig.decl == nil || sig.dict != nil || sig.tps != nil || funcHasDefault(sig.decl) {
		return no()
	}
	fk := funcKindIn(bl.g, sig.params, sig.result)
	if !irCallableValueKind(fk) {
		return no()
	}
	ctl := irNamedCtlForm(sig.decl)
	if ctl != irCtlNone && bl.ctlRefs[t] != ctl {
		irDeclineNote("an iter-sensitive function passed where its break or continue cannot be caught: " + t.Name)
		return no()
	}
	ref := ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irFunctionCallee(sig))
	bl.b.Append(ref)
	bl.side(ref.Dst(), irScalarSide{k: fk, ctl: ctl})
	return ref.Dst(), fk, true, true
}

// irDistinctParamBinding is the name a lambda parameter pattern `Dur(n)`
// binds when the parameter's kind is the wrapping distinct the pattern names;
// `_` for `Dur(_)`. Any other pattern answers false.
func irDistinctParamBinding(pattern ast.Node, k kind) (string, bool) {
	ep, ok := pattern.(*ast.EnumPattern)
	if !ok || k.tag != tagNamed || !irWrappingDistinct(k.def) {
		return "", false
	}
	owner, member, ok := patternHead(ep.Variant)
	if !ok || owner != "" || member != k.def.nomi {
		return "", false
	}
	if ep.Binding != "" && ep.Payload == nil {
		return ep.Binding, true
	}
	switch pl := ep.Payload.(type) {
	case *ast.IdentPattern:
		return pl.Name, true
	case *ast.WildcardPattern:
		return "_", true
	}
	return "", false
}

// irPatternHeadKind is the type a self-typing parameter pattern names
// (`Dur(n)` names Dur), or kindInvalid.
func (g *gen) irPatternHeadKind(pattern ast.Node) kind {
	ep, ok := pattern.(*ast.EnumPattern)
	if !ok {
		return kindInvalid
	}
	owner, member, ok := patternHead(ep.Variant)
	if !ok || owner != "" {
		return kindInvalid
	}
	d, found := g.namedType(member)
	if !found || !d.isDistinct {
		return kindInvalid
	}
	return named(d)
}

// irFlatTupleParam reports a tuple pattern of names and wildcards whose arity
// matches a tuple kind.
func irFlatTupleParam(tp *ast.TuplePattern, k kind) bool {
	if k.tag != tagTuple || k.comp == nil || len(k.comp.parts) != len(tp.Patterns) {
		return false
	}
	for _, c := range tp.Patterns {
		switch c.(type) {
		case *ast.IdentPattern, *ast.WildcardPattern:
		default:
			return false
		}
	}
	return true
}
