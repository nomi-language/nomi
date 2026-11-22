package irbuild

// Qualified calls resolve local impl members, sibling-file functions and
// concrete stdlib methods whose bodies are retained in the IR cache. Local
// declarations take precedence. Stdlib methods reuse the conservative function-
// reference resolver. Four String host functions have explicit VM adapters;
// other host, generic, defaulted and ambiguous targets remain outside this
// path. Map.get/put/size use the builtin container route and shared rt map
// operations. Vector length/at/push/concat share the builtin vector route.
// Set size/contains?/insert/remove share the builtin set route.
// Result.map_err shares its runtime driver, and concrete scalar to_string
// calls use the Display Render instruction.
// Range queries carry their comparator as an explicit function operand, except
// Float's interval predicate. The private string comparator uses a host binding.
// Erased dispatch on a local interface is a dispatched call through the
// interface's method table. Stdlib file APIs remain separate.
//
// Arguments are lowered in evaluation order, forcing every non-final impure
// value before the next argument's instructions are appended. Checked kinds
// then validate the selected signature without coercion or speculative
// lowering.
//
// A call names its declaring symbol, including across sibling and cached stdlib
// modules; approved rt bindings carry explicit host crossings.
//
// Calls inside recorded assertion subjects decline until qualified argument
// rows can preserve the complete operand history in reports.

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irQualPlan is one resolved qualified callee: an identity, a result kind, and
// how qualEmit appends the call.
//
// A PLAN RATHER THAN AN IMMEDIATE BUILD, because the operands are lowered
// BEFORE the callee is selected (irQualArgs), and a plan keeps "what did we
// resolve" separate from "what do we append".
type irQualPlan struct {
	// token is the declaration this package identifies the callee by, in
	// `irCalleeSym`'s sense: a `*stdFunc`, a `*fileFunc` or an `*implItem`,
	// each of which that function's header already lists.
	token  any
	name   string
	result kind
	host   bool
	// sym is the declaring identity of a sibling function or cached stdlib
	// body. Local impl calls instead intern their token in the current table.
	//
	// WHY A SECOND FIELD RATHER THAN A SECOND TOKEN CONVENTION. `ir.Table` is
	// per gen and `ir.Module.FuncFor` resolves by POINTER, so a cross-unit
	// callee needs the symbol the DECLARING unit will use — which is a
	// different table, not a different token. Carrying the resolved symbol is
	// the smallest way to say that; widening `ir.Table`'s scope to the whole
	// program would also move TYPE interning, and `irtable.go`'s
	// `irTypeOf` interns on a `kind` VALUE, so two units' `Int` would collapse
	// onto one `*ir.Type` and the `Conforms` edges would accumulate across
	// units. That is a change to overload selection's input for a linking
	// problem, and it is not needed for it.
	sym *ir.Symbol
	// dispatchAt is the receiver operand of a dispatched call, whose
	// implementation is selected by that operand's type at run time.
	dispatchAt int
	dispatch   bool
	// bound marks a call through a type parameter's bound in an abstract
	// body (boundCallPlan), which no operand selects an implementation of.
	bound bool
	// target is the declaration the plan lowers against when its token
	// does not name it (a std `host fn` bound by name, irExternBinding);
	// irQualPlanTarget answers it.
	target ast.Node
	// args, when set, replaces the call's lowered operands: a callee with
	// parameter defaults the call omits takes its written operands and then
	// each omitted default, evaluated at the call (fillCallDefaults).
	args *irQualArgs
}

// irQualArgs is the call's operands, lowered once.
//
// LOWERED BEFORE THE CALLEE IS SELECTED, which is forced rather than chosen:
// the routes after the record's choose by the operands' kinds (`byIface`
// needs the RECEIVER's kind to find the implementation at all, and
// stdIfacePlan picks among its overloads by them), and every route checks
// the operands against the callee's signature (qualSignature). So this
// producer cannot ask "is the callee resolvable" before asking "what do the
// operands lower to", which is the reverse of `bl.call`'s bare-callee order.
type irQualArgs struct {
	temps  []ir.Temp
	kinds  []kind
	mobile []bool
	// ok is false when any operand did not lower.
	ok bool
}

// recvKind is the kind of the call's first operand, its receiver for
// every interface function verifyCallForm asks about, or kindInvalid.
func (a irQualArgs) recvKind() kind {
	if !a.ok || len(a.kinds) == 0 {
		return kindInvalid
	}
	return a.kinds[0]
}

// irQualLowerArgs lowers every operand, answering whether all of them did.
func (bl *irScalarBuilder) irQualLowerArgs(t *ast.Call) irQualArgs {
	a := irQualArgs{
		temps:  make([]ir.Temp, len(t.Args)),
		kinds:  make([]kind, len(t.Args)),
		mobile: make([]bool, len(t.Args)),
		ok:     true,
	}
	// This call's own bare operands; a qualified call nested in an operand
	// sees none.
	bare := bl.qualBare
	bl.qualBare = nil
	for i, arg := range t.Args {
		var src ir.Temp
		var k kind
		var mobile, ok bool
		// kindInvalid: sentinel — the operand at this position is not bare.
		if i < len(bare) && bare[i] != kindInvalid {
			want := bare[i]
			src, k, mobile, ok = bl.preludeBareValue(arg, want)
		} else if want, typed := bl.qualCheckedBareWant(t, i, arg); typed {
			// A bare `None` operand takes its payload type from the
			// checker's instantiated parameter, as a `==` operand does.
			if src, k, mobile, ok = bl.preludeBareValue(arg, want); !ok {
				src, k, mobile, ok = bl.lower(arg)
			}
		} else if sl, want, wider := bl.qualCheckedLitWant(t, i, arg); wider {
			// `Own.get(Own{v: Spot{n: 3}})` where a Shape is expected: the
			// literal is built at the instance the checker instantiated
			// the parameter at, its field widened into the enum, as an
			// annotation's expected instance builds it.
			if src, k, mobile, ok = bl.genericLitWant(sl, want); !ok {
				src, k, mobile, ok = bl.lower(arg)
			}
		} else {
			src, k, mobile, ok = bl.lower(arg)
		}
		if !ok {
			a.kinds[i] = kindInvalid
			a.ok = false
			continue
		}
		if want, wider := bl.qualCheckedFuncWant(t, i, k); wider {
			// `Iter.map(lists, Iter.count)`: a function whose parameter is
			// wider than the one the call expects enters through an
			// adapter of the expected type (irfuncwiden.go), so the
			// callee's selection sees the type the checker solved.
			if adapted, widened := bl.funcWiden(arg, src, k, want); widened {
				src, k, mobile = adapted, want, false
			}
		}
		if irQualBoxCandidate(k) && bl.g.stdModule == "" {
			// `P.pair(p, [1, 2])` with a `Show` parameter: a value whose
			// runtime type does not name its impl enters boxed as the
			// checker recorded it (irboxed.go), so the callee's selection
			// sees the parameter. One that enters unboxed keeps its own
			// kind for the selection.
			if boxed, want, how := bl.coerceRecorded(argExpr(arg), src, k, kindInvalid); how == irCoercedBoxed {
				src, k, mobile = boxed, want, false
			}
		}
		// Force the value before the next argument's instructions are
		// appended. Delaying this copy until qualEmit would reorder effects.
		if !mobile && i != len(t.Args)-1 {
			cp := ir.NewCopy(bl.g.irNodePos(arg), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
			src = cp.Dst()
		}
		// `gen.callArgs` holds the final operand inside an assertion subject,
		// because its row names it a second time. Only a call whose rows
		// recordedQualCall records after it follows that path.
		if i == len(t.Args)-1 && bl.recording > 0 && bl.qualRecord == t && !irHeldValue(bl.f, src, bl.sides) {
			cp := ir.NewCopy(bl.g.irNodePos(arg), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyHold})
			src, mobile = cp.Dst(), true
		}
		a.temps[i], a.kinds[i] = src, k
		a.mobile[i] = mobile
	}
	if bl.qualRecord == t {
		bl.qualRecordArgs, bl.qualRecordSeen = a, true
	}
	return a
}

// stdInstRangeReceiver reports a std generic instance's call whose receiver
// is a Range (`List.inspect<Range<Int>>` rendering each element): Range's
// Display and Debug are std's generic impls, reached only through their own
// instance.
func (bl *irScalarBuilder) stdInstRangeReceiver(args irQualArgs) bool {
	if bl.g.stdInstCur == nil || !args.ok || len(args.kinds) == 0 {
		return false
	}
	return irContainerBaseName(args.kinds[0]) != ""
}

// stdContainsIface is std's Contains, whose container impls are the
// intrinsics a type-qualified call reaches.
var stdContainsIface = analysis.StdType{Module: "std/contains", Name: "Contains"}

// ifaceContainerCall lowers `Iface.method(x, ...)` whose receiver x is a std
// container or prelude enum (`Display.to_string(set)`,
// `Hashable.hash(Some(1))`), and a call the builder writes with the record
// of one (an operator's, an interpolation hole's), as the function of std's
// `impl Iface for Owner<T>` the record names at the instance (calleeAt):
// for std's Contains, the intrinsic the type-qualified call reaches
// (`Set.contains`), and otherwise that function's template instantiated at
// x's kind. A call qualified with a type is the type's routes'. The
// operands are the ones irQualLowerArgs already lowered.
func (bl *irScalarBuilder) ifaceContainerCall(t *ast.Call, args irQualArgs, method string, selfType analysis.Type) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	// Inside a std module only an instance body or a test body instantiates,
	// as an owner-qualified `Set.f(...)` call in a test body does.
	if (bl.g.stdModule != "" && !bl.stdInstRangeReceiver(args) && !bl.stdTestOwnType()) || !args.ok || len(args.kinds) == 0 {
		return no()
	}
	q := bl.g.routeCallee(t).RouteOwner()
	if q.Kind == analysis.QualType || q.Kind == analysis.QualTypeParam || q.Kind == analysis.QualModule {
		return no()
	}
	var d *ifaceDef
	isIface := false
	if std, named := bl.g.recordedStdIface(t); named {
		// The std interface the record names, by its identity: the def of
		// it a unit of the program declares (a std module lowered with the
		// program), whatever this file binds the spelling to.
		d, isIface = bl.g.solvedIface(std.Module, std.Name)
	} else {
		// A program's interface the record names, by its declaration.
		d = bl.g.recordedIface(t)
		isIface = d != nil
	}
	if isIface && d != nil && bl.g.stdInstCur == nil && !bl.g.stdCases {
		// A program's interface is dispatched through its own table. A std
		// instance's module, or the module whose cases these are, may
		// declare the interface its container impl implements
		// (`ToJson.to_json(item)` in `List.to_json<List<Note>>`), and that
		// impl is the template below.
		return no()
	}
	c := bl.g.calleeAt(t)
	self := args.kinds[0]
	base := irContainerBaseName(self)
	if c.Kind != analysis.CalleeImpl || c.Std.IsZero() || base == "" || base != c.Std.Owner {
		return no()
	}
	if q.Std == stdContainsIface && c.Std.Name == "contains" {
		var plan *irQualPlan
		switch base {
		case "Set":
			plan = bl.setCallPlan(t, args, method)
		case "Map":
			plan = bl.mapCallPlan(t, args, method)
		case "List":
			plan = bl.listCallPlan(t, args, method)
		case "Vector":
			plan = bl.vectorCallPlan(t, args, method)
		}
		if plan != nil {
			v, k, mobile, ok := bl.qualEmit(t, args, plan)
			return v, k, mobile, ok, true
		}
	}
	// A function that is no template (stdTemplateOf answers nil) is left
	// to the routes after, as stdInstCallAt answers for nil.
	prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
	bl.stdInstPreCall, bl.stdInstPreArgs = t, args
	defer func() { bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs }()
	return bl.stdInstCallAt(t, bl.g.stdInsts.stdTemplateOf(c.Std), self, selfType)
}

// qualCheckedBareWant is the checked parameter kind for operand i when that
// operand is a payload-free prelude variant written bare (`None`), and the
// checker instantiated the parameter to a retained prelude enum.
func (bl *irScalarBuilder) qualCheckedBareWant(t *ast.Call, i int, arg ast.Node) (kind, bool) {
	if _, _, _, bare := preludeValueName(arg); !bare {
		return kindInvalid, false
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || i >= len(ft.Params) {
		return kindInvalid, false
	}
	want := bl.g.project(ft.Params[i])
	if want.tag != tagNamed || want.def == nil || want.def.preludeOf == nil || !irRetainedEnumKind(want.def) {
		return kindInvalid, false
	}
	return want, true
}

// qualCheckedLitWant is the generic instance the checker instantiated
// parameter i of the call at, when operand i is a struct literal of that
// generic type the checker typed at another instance: one whose type
// argument is a type an enum `embeds`, where the parameter's is the enum.
func (bl *irScalarBuilder) qualCheckedLitWant(t *ast.Call, i int, arg ast.Node) (*ast.StructLit, kind, bool) {
	sl, isLit := arg.(*ast.StructLit)
	if !isLit {
		return nil, kindInvalid, false
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || i >= len(ft.Params) {
		return nil, kindInvalid, false
	}
	want := bl.g.project(ft.Params[i])
	if want.tag != tagNamed || want.def == nil || want.def.genericOf == nil {
		return nil, kindInvalid, false
	}
	if have := bl.g.checkedKind(arg); have == want || have.def == nil || have.def.genericOf != want.def.genericOf {
		return nil, kindInvalid, false
	}
	return sl, want, true
}

// qualCheckedFuncWant is the function type the checker instantiated
// parameter i of the call at, when operand i is a function value of kind k
// of the same arity whose parameters differ from it: wider than the call's.
func (bl *irScalarBuilder) qualCheckedFuncWant(t *ast.Call, i int, k kind) (kind, bool) {
	if k.tag != tagFunc || k.comp == nil {
		return kindInvalid, false
	}
	// A non-generic callee (`Route.to(route, handler: Handler)`) has no
	// instantiated signature; its declared one is the parameter's type.
	ft := bl.g.checkedCallSignature(t)
	if ft == nil {
		ft = bl.g.declaredCallSignature(t)
	}
	if ft == nil || i >= len(ft.Params) {
		return kindInvalid, false
	}
	want := bl.g.project(ft.Params[i])
	if want == k || want.tag != tagFunc || want.comp == nil || len(funcParams(want)) != len(funcParams(k)) {
		return kindInvalid, false
	}
	// Only a parameter makes the value need an adapter here: a callback
	// whose result is a List where an Iter is expected (`Iter.flat_map(xs,
	// fn x -> [x])`) is read by the operation as it is.
	for i, p := range funcParams(want) {
		if p != funcParams(k)[i] {
			return want, true
		}
	}
	// A result that erases into an interface the operation calls through
	// (a handler answering a `Result` where std::http's `ToResponse` is
	// expected) needs the adapter too, as an element of a list of them
	// gets one (irlist.go).
	if r := funcResult(want); r.tag == tagIface && funcResult(k) != r {
		return want, true
	}
	return kindInvalid, false
}

// irQualBoxCandidate reports whether an operand of kind k of a qualified
// call enters its interface parameter boxed before the callee is selected:
// a std container or an `embeds` enum, whose runtime value names no impl
// of the interface.
func irQualBoxCandidate(k kind) bool {
	_, _, container := stdRecvParts(k)
	return container || irBoxedEnum(k)
}

// declaredCallSignature is the callee's declared function type, for a
// non-generic callee whose reference carries no instantiated CallType.
func (g *gen) declaredCallSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || t.Func == nil {
		return nil
	}
	line, col := calleeRefPos(t.Func)
	sym := g.srcFA().References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return nil
	}
	ft, _ := sym.Type.(*analysis.FuncType)
	return ft
}

// qualCall lowers a call whose callee is written QUALIFIED.
//
// `io.print` and `io.inspect` are answered by `bl.hostOutput` first, because
// their operand is the RENDERED TEXT rather
// than the argument — `ir.Render` sits between the two — and no signature in
// any index says so.
func (bl *irScalarBuilder) qualCall(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	if bl.qualCalleeIsValue(t, fa) {
		// `p.t.first(8)`, `gen.run(seed)`: a function read by a field chain,
		// called through the value it holds. An indirect call records its
		// own rows in an assertion subject.
		return bl.indirectCall(t)
	}
	if bl.recording > 0 {
		// Inside an assertion subject the call's rows are recorded after the
		// call (recordedQualCall).
		if route, _ := bl.g.recordedQualRoute(t, fa.Field.Name); route == routeIterLoop {
			// `Iter.loop(...)`, as its record names it: the callback is
			// inlined and records nothing; the loop's answer is the row its
			// enclosing operand records.
			return bl.iterLoop(t)
		}
		return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) { return bl.qualCallLowered(t, fa) })
	}
	return bl.qualCallLowered(t, fa)
}

// qualCallLowered is qualCall once the assertion-subject question is settled.
func (bl *irScalarBuilder) qualCallLowered(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if fa.Field == nil {
		// A parse artifact; `gen.fieldAccess` reports `field access` for it.
		panic("irbuild: qualCallLowered: parser always sets FieldAccess.Field")
	}
	method := fa.Field.Name
	obj, isIdent := ast.QualifierIdent(fa.Object)
	ti, isType := fa.Object.(*ast.TypeIdent)
	if isType && method == "unchecked" && len(t.Args) == 1 {
		if bl.g.refinementAt(ti.Line, ti.Col) {
			// A subtype's `unchecked` is its value, which is its base's
			// (uncheckedRefinement): `ClosedRange.unchecked(r)` in a std
			// body the shared lowering retains, which builds no instance
			// of the generic `unchecked`.
			return bl.lower(t.Args[0])
		}
	}
	if isType || !isIdent {
		// `Duration.seconds(1)`, `It.count(xs)` for an alias of Iter,
		// `json.Json.parse(s)`, `db::schema.Post.blank()`: the function of
		// the type, interface or type parameter the checker resolved the
		// qualifier to (analysis.Callee.RouteOwner), however the call
		// spells it and whatever else this file binds the spelling to. A
		// chain that names a value qualCall sent to indirectCall
		// (qualCalleeIsValue).
		c := bl.g.routeCallee(t)
		switch c.RouteOwner().Kind {
		case analysis.QualType, analysis.QualInterface, analysis.QualTypeParam:
			return bl.typeQualCall(t, method)
		}
		// The checker records what every qualified call is qualified with
		// (TestElaborationGate).
		bl.g.noteUnreadCall(t, c)
		return no()
	}
	bl.qualPreResolve(t, obj, false, fa.Field)
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	args := bl.irQualLowerArgs(t)
	if bl.namesLocal(obj) {
		// Shape C: a bound local's member holding a callable, which is
		// `CalleeIndirect` and a different resolution.
		return no()
	}
	if v, rk, mobile, ok, handled := bl.variantNameCall(t, obj, method); handled {
		return v, rk, mobile, ok
	}
	if v, rk, mobile, ok, handled := bl.stdGenericFileCall(t); handled {
		return v, rk, mobile, ok
	}
	plan := bl.qualFilePlan(t, args, obj, method)
	if plan == nil {
		return no()
	}
	return bl.qualEmit(t, args, plan)
}

// markSignalCallback widens cb, an unseeded iteration operation's callback,
// to the adapter's signalling form when its own body can signal, and
// answers the undo: a lambda (markCtl), or a capture (markCtlCapture). A
// callback of any other shape whose body would signal cannot be widened.
func (bl *irScalarBuilder) markSignalCallback(cb ast.Node) (func(), bool) {
	if !bl.g.callbackCarriesSignal(cb) {
		return func() {}, true
	}
	switch cb := cb.(type) {
	case *ast.Lambda:
		return bl.markCtl(cb, irCtlAdapter), true
	case *ast.Capture:
		// `Iter.map(xs, &keep(&1))`: the lambda the capture lowers as.
		return bl.markCtlCapture(cb), true
	}
	panic(fmt.Sprintf("irbuild: markSignalCallback: reached with %T, where callbackCarriesSignal answers only for a lambda or a capture", cb))
}

// typeQualCall lowers a call qualified with a type, an interface or a type
// parameter (typeQualRoutes), and checks the value a reader's function
// makes at a subtype (readNarrowed).
func (bl *irScalarBuilder) typeQualCall(t *ast.Call, method string) (ir.Temp, kind, bool, bool) {
	read := bl.subtypeReadOf(t)
	v, k, mobile, ok := bl.typeQualRoutes(t, method)
	if read == nil || !ok {
		return v, k, mobile, ok
	}
	return bl.readNarrowed(t, read, v, k)
}

// typeQualRoutes lowers `Owner.method(args)`, a call of a function of the
// type, interface or type parameter its record says it is qualified with
// (analysis.Callee.RouteOwner): a qualified call however it spells its
// owner (qualCallLowered), and a bare call of a function declared under a
// type or an interface, whose record names that owner (bareImplCall,
// bareStdMethodCall). Every fact it reads about the call is keyed on t,
// the call as written, and nothing about it is read from the owner's
// spelling.
func (bl *irScalarBuilder) typeQualRoutes(t *ast.Call, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	route, routeKey := bl.g.recordedQualRoute(t, method)
	signalOwner := len(t.Args) == 2 && irSignallingCallback(routeKey)
	if signalOwner {
		// The operations the VM drives a signalling callback through.
		if ref := irFuncRefNode(argExpr(t.Args[1])); ref != nil {
			defer bl.markCtlRef(ref)()
		}
	}
	switch route {
	case routeIterSeeded:
		return bl.iterReduceCall(t, method)
	case routeStructUpdate:
		return bl.structUpdateCall(t)
	}
	if signalOwner && !irSeededIterMethod(method) {
		undo, ok := bl.markSignalCallback(argExpr(t.Args[1]))
		if !ok {
			return no()
		}
		defer undo()
	}
	switch route {
	case routeIterLoop:
		// The callback is inlined rather than lowered as a value.
		return bl.iterLoop(t)
	case routeContextValue:
		return bl.contextValueCall(t, method)
	}
	bl.qualPreResolve(t, nil, true, nil)
	if v, rk, mobile, ok, handled := bl.interfaceCall(t); handled {
		// An operator interface's function, by the record's form
		// (ifacecall.go).
		return v, rk, mobile, ok
	}
	switch route {
	case routeToJson:
		// A record the call's record answers structurally (irrecordjson.go).
		if v, rk, mobile, ok, handled := bl.recordToJsonCall(t, method); handled {
			return v, rk, mobile, ok
		}
	case routePreludePredicate:
		if len(t.Args) == 1 {
			return bl.preludePredicateCall(t, routeKey)
		}
	}
	if q := bl.g.routeCallee(t).RouteOwner(); q.Kind == analysis.QualTypeParam && q.Param != nil {
		if k, bound := bl.g.genericSubstKind(q.Param.Name); bound {
			// `T.from_json(item)` inside an instance: the record's owner
			// is T, which the instance binds to a concrete argument, and
			// the call is that type's.
			kt, _ := bl.g.genericSubstType(q.Param.Name)
			if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, kt, method); handled {
				return v, rk, mobile, ok
			}
			if !irScalarLeafKind(k) {
				return no()
			}
			// At a scalar the call is planned by the routes below from
			// its record at the instance, and routed as a call qualified
			// with the scalar is (qualRoute).
		}
	}
	if v, rk, mobile, ok, handled := bl.stdGenericQualCall(t, method); handled {
		return v, rk, mobile, ok
	}
	args := bl.qualCallArgs(t)
	var plan *irQualPlan
	if route == routeScalarText || route == routeDisplay {
		if v, ok, handled := bl.displayCall(t, args); handled {
			return v, kindString, false, ok
		}
	}
	if route == routeIter {
		return bl.iterCall(t, args, method, irIterSourceArg(t))
	}
	if route == routeEachWhile && args.ok && len(args.kinds) == 2 &&
		((routeKey.Owner == stdBytesType && args.kinds[0] == irBytesKind()) || (routeKey.Owner == stdStringType && args.kinds[0] == kindString)) {
		// `Bytes.each_while(b, f)` / `String.each_while(s, f)`: the impl's
		// host fn is the protocol the VM's byte and grapheme views already
		// drive, so the type-qualified call is `Iter.each_while` over that
		// view rather than a crossing no host table answers.
		return bl.iterCall(t, args, "each_while", nil)
	}
	if route == routeStringJoin && args.ok && (len(args.kinds) == 1 || len(args.kinds) == 2) {
		// `String.join(parts, separator)` drives any Iter<String> once,
		// so it is an Iter terminal over the source's view. The
		// separator's default is "".
		if len(args.kinds) == 1 {
			sep := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), "")
			bl.b.Append(sep)
			bl.side(sep.Dst(), irScalarSide{k: kindString})
			args = irQualArgs{
				temps:  []ir.Temp{args.temps[0], sep.Dst()},
				kinds:  []kind{args.kinds[0], kindString},
				mobile: []bool{args.mobile[0], true},
				ok:     true,
			}
		}
		return bl.iterCall(t, args, "join", irIterSourceArg(t))
	}
	if route == routeRange && !bl.callsProgramFunc(t) {
		return bl.rangeCall(t, args, method)
	}
	if route == routeSet && stdIterMember(bl.g.routeCallee(t).Std) {
		// `impl Iter for Set<T>`'s members, named by the type: the Iter
		// protocol over the Set's view, as a Range's are (rangeCall). Its
		// body reads the `items` map the machine's Set does not have.
		return bl.iterCall(t, args, method, nil)
	}
	if v, k, mobile, by := bl.inspectCall(t, args, method); by != "" {
		bl.g.noteCallRoute(t, by)
		return v, k, mobile, true
	}
	if v, k, mobile, ok, handled := bl.ifaceContainerCall(t, args, method, bl.g.firstOperandType(t)); handled {
		bl.g.noteCallRoute(t, viaIfaceContainer)
		bl.g.verifyCallForm(t, args.recvKind(), "ifaceContainerCall", analysis.OperatorByImpl)
		return v, k, mobile, ok
	}
	owner := bl.g.qualOwnerName(t)
	if p := bl.stdRecvOwnerPlan(t, args, owner, method); p != nil {
		// `List.show(xs)`: the program's impl over a std container.
		plan = p
	} else if route == routeMapErr {
		plan = bl.resultMapErrPlan(t, args)
	} else if route == routeToResult {
		plan = bl.preludeToResultPlan(t, args)
	} else if route == routeWithDefault {
		if args.ok && len(args.kinds) == 2 && args.kinds[1] == kindUnit {
			// `Result.with_default(r, Unit)` over a `Result<Unit, E>`: both
			// outcomes are the one Unit value, so the call is Unit once its
			// operands, lowered above, have run. The crossing below takes
			// no Unit payload.
			u := ir.NewUnit(bl.g.irNodePos(t), bl.f.NewTemp())
			bl.b.Append(u)
			return u.Dst(), kindUnit, true, true
		}
		plan = bl.preludeWithDefaultPlan(t, args, routeKey.Owner.Name)
	} else if route == routeContainerCompare {
		return bl.containerCompareAny(t, args, routeKey.Owner.Name)
	} else if route == routeMap {
		plan = bl.mapCallPlan(t, args, method)
	} else if route == routeList {
		plan = bl.listCallPlan(t, args, method)
	} else if route == routeVector {
		plan = bl.vectorCallPlan(t, args, method)
	} else if route == routeSet {
		plan = bl.setCallPlan(t, args, method)
	} else {
		plan = bl.qualImplPlan(t, args, owner, method)
	}
	if plan == nil {
		// `Int.min(3, 4)`, `Comparable.min(v, w)`: a std interface's
		// default that no impl of the receiver writes, instantiated in its
		// declaring module (stdifacedefault.go).
		if v, rk, mobile, ok, handled := bl.stdIfaceDefaultCall(t, args); handled {
			bl.g.noteCallRoute(t, viaStdIfaceDefault)
			return v, rk, mobile, ok
		}
	}
	container := route == routeMap || route == routeSet || route == routeVector || route == routeList
	if plan == nil && container {
		// A Map, Set, Vector or List function with no intrinsic is std's
		// Nomi body, instantiated at the call's types like any generic std
		// member, from a program's body or from a std body
		// (`List.slice` calling `List.length`).
		// kindInvalid: sentinel — no receiver kind selects the member.
		self := kindInvalid
		var selfType analysis.Type
		if args.ok && len(args.kinds) > 0 && irContainerBaseName(args.kinds[0]) == routeKey.Owner.Name {
			// An impl member (`impl Display for Vector<T>`) is solved
			// from its receiver, which the first operand is.
			self, selfType = args.kinds[0], bl.g.operandCheckerType(t.Args[0])
			// kindInvalid: sentinel — solvedParamKind's nothing-recorded answer; the receiver keeps the operand's kind.
			if want := bl.g.solvedParamKind(t, method, 0); want != kindInvalid && irContainerBaseName(want) == routeKey.Owner.Name {
				// The receiver at the checker's instantiation, which a
				// later operand may have widened past the first one's
				// (`Set.union(circles, shapes)` is a Set<Shape>); the
				// instance converts the operand as its parameter.
				self, selfType = want, bl.g.solvedParamType(t, method, 0)
			}
		}
		prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
		bl.stdInstPreCall, bl.stdInstPreArgs = t, args
		v, rk, mobile, ok, handled := bl.stdInstCallAt(t, bl.g.stdInsts.stdTemplateOf(bl.g.calleeAt(t).Std), self, selfType)
		bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs
		if handled {
			return v, rk, mobile, ok
		}
	}
	if plan == nil {
		return no()
	}
	return bl.qualEmit(t, args, plan)
}

// qualPreResolve resolves the qualifier of the call t before the arguments
// are lowered. Resolving a sibling file's interface (the one the record
// says t is qualified with, recordedIface) or `fn` (the one field, the
// call's member, names) imports the kinds its signature names, in the
// order it happens, so they are imported before any argument needs them.
func (bl *irScalarBuilder) qualPreResolve(t *ast.Call, obj *ast.Ident, isType bool, field *ast.Ident) {
	g := bl.g
	if isType {
		g.recordedIface(t)
		return
	}
	if g.files == nil || bl.namesLocal(obj) {
		return
	}
	to, isSibling := g.lookupQualifier(obj)
	if !isSibling {
		return
	}
	f := g.siblingFuncAt(to, field)
	if f == nil || !f.lowerable() || f.private && !g.partnerTestOf(to) {
		return
	}
	for _, p := range f.params {
		if _, ok := g.importKind(p); !ok {
			return
		}
	}
	g.importKind(f.result)
}

// checkedBareOperands types each payload-free prelude variant operand of a
// qualified call (`Maybe.with_default(Maybe.None, 0)`) by the parameter type
// the checker solved for the call, which the variant's own reference does not
// carry. An operand the solved signature does not type as an instance of the
// same prelude enum is left to ordinary lowering (kindInvalid), and nil means
// no operand is one.
func (bl *irScalarBuilder) checkedBareOperands(t *ast.Call) []kind {
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != len(t.Args) {
		return nil
	}
	var bare []kind
	for i, arg := range t.Args {
		name, line, col, ok := preludeValueName(arg)
		if !ok {
			continue
		}
		a, _, vs, ok := bl.g.resolvedPreludeVariant(name, line, col)
		if !ok || vs.carries() {
			continue
		}
		k := bl.g.project(ft.Params[i])
		if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || k.def.preludeOf.spec != a.spec ||
			!irRetainedEnumKind(k.def) {
			continue
		}
		if bare == nil {
			bare = make([]kind, len(t.Args))
			for j := range bare {
				// kindInvalid: sentinel — the operand at this position is not bare.
				bare[j] = kindInvalid
			}
		}
		bare[i] = k
	}
	return bare
}

// qualImplPlan resolves `Owner.method(args)`: from the function the call's
// record names (recordedCallPlan), else, for a call the record leaves to
// dispatch where it runs on a receiver held as an interface value, from the
// interface function it names (dispatchedCallPlan), else, for a call the
// record qualifies with an interface this gen holds a def of, from that
// interface by its declaration and the receiver's kind (recordedIface,
// concreteInterfacePlan), else from std's index by the receiver's kind
// (stdIfacePlan), for a call whose record names an interface's function
// and no type the builder can read the receiver's impl from (irbuild
// package doc, "Qualified calls from their records").
func (bl *irScalarBuilder) qualImplPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	key := owner + "." + method
	if p := bl.recordedCallPlan(t, args, key); p != nil {
		return bl.planned(t, viaByRecord, p)
	}
	if p := bl.dispatchedCallPlan(t, args); p != nil {
		// A receiver held as an interface value: dispatched on its
		// run-time type through the function the record names; in an
		// abstract body, a function no one receiver selects through the
		// bound (boundcall.go).
		return bl.planned(t, viaByDispatch, p)
	}
	if d := bl.g.recordedIface(t); d != nil {
		return bl.planned(t, viaConcreteIface, bl.concreteInterfacePlan(t, args, d, method))
	}
	return bl.planned(t, viaStdIface, bl.stdIfacePlan(t, args, owner, method))
}

// implItemPlan is the call of one impl function the call t names, its
// omitted defaulted operands filled and its embedded operands widened.
func (bl *irScalarBuilder) implItemPlan(t *ast.Call, args irQualArgs, only *implItem, key string) *irQualPlan {
	var filled *irQualArgs
	if args.ok && only.lowerable && bl.g.leavesDefaults(t, len(only.params)) {
		// `Box.announce(b)` omitting a defaulted parameter.
		fd := irImplSource(only)
		if fd == nil || namedArgNode(t.Args) != nil {
			return nil
		}
		full, ok := bl.filledOperands(t, args, only.params, fd.Params, defaultHome{})
		if !ok {
			return nil
		}
		filled = &full
		args = full
		t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
	}
	if widened, changed := bl.qualWidenEmbeds(t, args, only.params); changed {
		// `Shape.to_string(circle)`: the Circle enters its
		// `embeds` variant.
		filled = &widened
		args = widened
	}
	if !bl.qualSignature(t, args, only.params, only.result) || !only.lowerable {
		return nil
	}
	return &irQualPlan{token: only, name: key, result: only.result, args: filled}
}

// qualSiblingImplPlanFor resolves `Owner.method(args)` to an impl function
// another file declares for d, the owner's def, which a kind names even
// where no import brings its name into scope: the one written site outside
// this unit, with the declaring file's kinds translated into this one's
// before the signature is compared. Anything that would need a refusal or
// a coercion declines.
func (bl *irScalarBuilder) qualSiblingImplPlanFor(t *ast.Call, args irQualArgs, d *typeDef, method string) *irQualPlan {
	return bl.qualSiblingIfaceImplPlan(t, args, d, method, "")
}

// qualSiblingIfaceImplPlan is qualSiblingImplPlanFor narrowed to the impls of
// one interface, the one whose key is iface (ifaceKey), when iface is not
// empty: `Loud.say(q)` where a sibling file
// implements both `Loud` and `Soft` with a `say` for q's type, and `compare`
// named by both `impl Comparable` and `impl Ranked` is Comparable's for a
// sort.
func (bl *irScalarBuilder) qualSiblingIfaceImplPlan(t *ast.Call, args irQualArgs, d *typeDef, method, iface string) *irQualPlan {
	g := bl.g
	if g.files == nil || g.fileUnit < 0 || g.reg == nil {
		return nil
	}
	if d == nil || !d.lowerable || d.decl == nil {
		return nil
	}
	if d.instOrigin != nil || d.genericOf != nil {
		// A generic instance, another file's or this one's: its impls are
		// registered per instance, not at the template's written sites.
		return bl.siblingInstanceImplPlan(t, args, d, method, iface)
	}
	site := g.siblingImplSiteBy(d.decl, method, iface, bl.siblingInstPick(t))
	if site == nil {
		return nil
	}
	return bl.siblingSitePlan(t, args, site, d.nomi, method)
}

// siblingSitePlan is the call t of the impl function site, which another
// file declares for the type named recvName: a generic member that file
// instantiates for the call, or a monomorphic one called in the declaring
// unit, its kinds translated into this one.
func (bl *irScalarBuilder) siblingSitePlan(t *ast.Call, args irQualArgs, site *implMemberSite, recvName, method string) *irQualPlan {
	g := bl.g
	if site.withheld != nil {
		// `leaf.Box.ident(6)`: a generic member, instantiated by its file.
		if site.fn.why != "generic impl function" {
			// A private inherent member, which the checker already refuses.
			return nil
		}
		return bl.siblingMethodPlan(t, args, g.reg.gens[site.unit], site.withheld, method)
	}
	chosen := site
	params, result, owning := g.siblingSiteKinds(site)
	if owning == nil {
		return nil
	}
	f := chosen.fn
	if f.defaults && namedArgNode(t.Args) != nil {
		return nil
	}
	to := chosen.unit
	var filled *irQualArgs
	if f.defaults && args.ok && bl.g.leavesDefaults(t, len(params)) {
		// `Powered.describe(e)` omitting a parameter the declaring file
		// defaults: the default is built here, when every name it mentions
		// resolves to the same declaration from both files. A concrete
		// operand of an interface-typed slot enters it first, since the
		// slotting matches kinds exactly.
		if erased, changed := bl.qualEraseSlotted(t, args, params, f.params0); changed {
			args = erased
		}
		full, ok := bl.filledOperands(t, args, params, f.params0, chosen.defaultHome(owning))
		if !ok {
			return nil
		}
		filled = &full
		args = full
		t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
	}
	if widened, changed := bl.qualWidenEmbeds(t, args, params); changed {
		// `sql.Statement.join([a, b], sep)`: the list enters its
		// `Iter<Statement>` parameter as the sequence.
		filled = &widened
		args = widened
	}
	if !bl.qualSignature(t, args, params, result) {
		return nil
	}
	unit := g.files.units[to]
	p := &irQualPlan{token: f, name: unit.key + "." + recvName + "." + method, result: result, args: filled}
	p.sym = owning.irImplSym(chosen.item)
	return p
}

// siblingImplSiteBy is the impl site of method, of the interface whose key
// is iface (any interface when iface is empty), that another file writes
// for the receiver declared by recv. Several sites of one interface are
// its impls at several type arguments, and pick, when not nil, chooses the
// call's (instimpl.go); several sites otherwise decline rather than let
// ordering decide. The sites are keyed by the receiver's declaration, so
// every spelling of the receiver (`Box`, `lib.Box`) finds the same ones.
func (g *gen) siblingImplSiteBy(recv ast.Node, method, iface string, pick func([]implMemberSite) *implMemberSite) *implMemberSite {
	if g.files == nil || g.fileUnit < 0 || g.reg == nil || recv == nil {
		return nil
	}
	var sites []implMemberSite
	for _, site := range writtenImplMembers(g.files.implMembers[implMemberKey{recv: recv, method: method}]) {
		if site.unit == g.fileUnit || (iface != "" && site.iface != iface) {
			continue
		}
		sites = append(sites, site)
	}
	switch {
	case len(sites) == 1:
		return &sites[0]
	case len(sites) > 1 && pick != nil && site0Iface(sites):
		return pick(sites)
	}
	return nil
}

// site0Iface reports whether every site implements one interface.
func site0Iface(sites []implMemberSite) bool {
	for _, s := range sites {
		if s.iface == "" || s.iface != sites[0].iface {
			return false
		}
	}
	return true
}

// siblingImplMember is the one impl function another file declares for its
// type d under `method`, narrowed to the impls of the interface whose key
// is iface (ifaceKey) when iface is not empty,
// with its parameter and result kinds translated into this unit and the gen
// that declares it. A nil site is no such function, more than one (native
// reports `ambiguous type-qualified call`), or one this unit cannot type.
func (g *gen) siblingImplMember(d *typeDef, method, iface string) (*implMemberSite, []kind, kind, *gen) {
	if d == nil {
		return nil, nil, kindInvalid, nil
	}
	return g.siblingImplMemberBy(d.decl, method, iface, nil)
}

// siblingImplMemberBy is siblingImplMember for the receiver declared by
// recv, choosing among its impls of one interface at several type
// arguments with pick.
func (g *gen) siblingImplMemberBy(recv ast.Node, method, iface string, pick func([]implMemberSite) *implMemberSite) (*implMemberSite, []kind, kind, *gen) {
	chosen := g.siblingImplSiteBy(recv, method, iface, pick)
	if chosen == nil {
		return nil, nil, kindInvalid, nil
	}
	params, result, owning := g.siblingSiteKinds(chosen)
	if owning == nil {
		return nil, nil, kindInvalid, nil
	}
	return chosen, params, result, owning
}

// siblingSiteKinds is the parameter and result kinds of the impl function
// site, which another file declares, translated into this unit, and the
// gen that declares it; owning is nil when the site has no item this unit
// can call or type.
func (g *gen) siblingSiteKinds(site *implMemberSite) (params []kind, result kind, owning *gen) {
	if site.item == nil || !site.fn.lowerable() {
		return nil, kindInvalid, nil
	}
	f := site.fn
	params = make([]kind, len(f.params))
	for i, p := range f.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil, kindInvalid, nil
		}
		params[i] = ip
	}
	result, ok := g.importKind(f.result)
	if !ok {
		return nil, kindInvalid, nil
	}
	if owning = g.reg.gens[site.unit]; owning == nil {
		return nil, kindInvalid, nil
	}
	return params, result, owning
}

// slottedOperands places a short call's written operands, already lowered
// in written order (irQualLowerArgs), at the parameter slots a direct call
// gives them (callPlan): positionally, except that a trailing callback
// goes to the last slot past defaulted parameters, as in
// `Actor.call(actor, fn reply -> .Get{reply:})` skipping `timeout`. A slot no
// operand fills is ir.NoTemp, for fillCallDefaults. ok is false when an
// operand's kind is not its slot's.
func (g *gen) slottedOperands(t *ast.Call, args irQualArgs, params []kind, decl []ast.Param) (temps []ir.Temp, mobile []bool, ok bool) {
	if !args.ok || len(args.temps) != len(t.Args) || len(decl) != len(params) {
		return nil, nil, false
	}
	plan, planned := g.callPlan(t, len(params))
	if !planned {
		return nil, nil, false
	}
	temps = make([]ir.Temp, len(params))
	mobile = make([]bool, len(params))
	for i := range temps {
		temps[i] = ir.NoTemp
	}
	for i, slot := range plan.slots {
		if slot < 0 || args.kinds[i] != params[slot] || !irCallOperandKind(args.kinds[i]) {
			return nil, nil, false
		}
		temps[slot] = args.temps[i]
		if i < len(args.mobile) {
			mobile[slot] = args.mobile[i]
		}
	}
	return temps, mobile, true
}

// qualFilePlan resolves `owner.method(args)` where owner is a FILE: a sibling
// file of this Nomi module, or a stdlib file's API object.
//
// SIBLING BEFORE STDLIB, which is `qualifiedCall`'s own order and its own
// reason — "`io` is a stdlib file and never resolves here".
func (bl *irScalarBuilder) qualFilePlan(t *ast.Call, args irQualArgs, ownerID *ast.Ident, method string) *irQualPlan {
	if to, isSibling := bl.g.lookupQualifier(ownerID); isSibling {
		return bl.qualSiblingPlan(t, args, to)
	}
	// Stdlib file APIs have separate generic, testing and host-call routes.
	// They are outside the concrete type-qualified method path, except the
	// hosts with VM adapters (RtFuncs rows, with or without the frame).
	_, isStd := bl.g.stdFileQualifier(ownerID)
	if !isStd {
		_, isStd = bl.stdOwnFileQualifier(ownerID)
	}
	if !isStd {
		return bl.ownSiblingPlan(t, args, ownerID)
	}
	return bl.stdFilePlan(t, args)
}

// stdFilePlan plans a call to a std file's top-level function, written
// `io.read_line()` or bare after `import std::io.read_line`: the function
// the call's record names.
func (bl *irScalarBuilder) stdFilePlan(t *ast.Call, args irQualArgs) *irQualPlan {
	// A host function by whichever row answers it (an rt function, with or
	// without the frame, a Go-backed adapter, a std/compiler crossing), or a
	// Nomi body (`json.shape_error_root`): the std module's cached function,
	// as derive synthesis calls it.
	return bl.stdFuncPlan(t, args, bl.g.stdFuncByID(bl.g.calleeAt(t).Std))
}

// irFrameHost admits an RtFuncs row that takes the caller's frame: the VM's
// generated adapter passes the call's runtime frame, so a cancellation reaches
// the rt function.
func irFrameHost(f *stdFunc) bool {
	_, row := stdlibHostFunc(f.key)
	return row && f.rtFrame
}

// qualSiblingPlan resolves a sibling file's top-level `fn` called with every
// argument written positionally. A call that names an argument or omits a
// defaulted one is siblingCall's, which bl.call routes it to first.
func (bl *irScalarBuilder) qualSiblingPlan(t *ast.Call, args irQualArgs, to int) *irQualPlan {
	unit := bl.g.files.units[to]
	var f *fileFunc
	if fa, qualified := t.Func.(*ast.FieldAccess); qualified {
		f = bl.g.siblingFuncAt(to, fa.Field)
	}
	if bl.g.siblingPrivate(to, f) {
		return nil
	}
	if f != nil && f.generic {
		params, result, sym, ok := bl.g.siblingGenericInstance(t.Func, to, f)
		if !ok || !bl.qualSignature(t, args, params, result) {
			return nil
		}
		return &irQualPlan{token: f, name: unit.key + "." + f.name, result: result, sym: sym}
	}
	if f == nil || !f.lowerable() {
		// No such `fn`, or a refused one.
		return nil
	}
	params, result, ok := bl.g.siblingSignature(f)
	if !ok || !bl.qualSignature(t, args, params, result) {
		return nil
	}
	return bl.g.siblingFuncPlan(to, f, result)
}

// siblingSignature is a sibling `fn`'s parameter and result kinds, translated
// from the declaring unit into this one (importKind).
func (g *gen) siblingSignature(f *fileFunc) ([]kind, kind, bool) {
	params := make([]kind, len(f.params))
	for i, p := range f.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil, kindInvalid, false
		}
		params[i] = ip
	}
	result, ok := g.importKind(f.result)
	return params, result, ok
}

// siblingFuncPlan is the plan for a call to unit `to`'s `fn` f.
func (g *gen) siblingFuncPlan(to int, f *fileFunc, result kind) *irQualPlan {
	unit := g.files.units[to]
	// BOTH IDENTITIES, and the second is the fallback's input rather than a
	// receipt: `qualEmit` reads `token`/`name` when `sym` is nil, which is the
	// no-registry path this file's `irSiblingCalleeSym` fences. Dropping them
	// here would make that path build a call with no callee at all.
	p := &irQualPlan{token: f, name: unit.key + "." + f.name, result: result}
	p.sym = g.irSiblingCalleeSym(to, f)
	return p
}

// siblingCall lowers a call to a sibling file's `fn`: a bare call to a
// selectively imported one (`import api.{make}` then `make("Ada")`, or an
// alias of it), found by its declaration node so an alias resolves, and a
// qualified one that names an argument or calls a function with defaults
// (`api.make("Ada", greeting: "Hi")`, siblingQualSite). Arguments are placed
// and lowered as bl.call places a local callee's (callPlan), and each
// omitted parameter is filled by the declaring unit's accessor for it
// (fillFromAccessors); the call links to the declaring unit's symbol as
// qualSiblingPlan's does.
func (bl *irScalarBuilder) siblingCall(t *ast.Call, site fileSite) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	f := site.fn
	if f == nil || f.decl == nil || bl.g.siblingPrivate(site.unit, f) {
		return no()
	}
	var params []kind
	var result kind
	var sym *ir.Symbol
	ok := false
	if f.generic && !site.host {
		params, result, sym, ok = bl.g.siblingGenericInstance(t.Func, site.unit, f)
	} else if f.lowerable() {
		params, result, ok = bl.g.siblingSignature(f)
	}
	if !ok || len(params) != len(f.decl.Params) {
		return no()
	}
	// The sibling's signature, held where it was declared.
	irHeldOperand(result, "a sibling function's result")
	names := make([]string, len(f.decl.Params))
	for i, p := range f.decl.Params {
		names[i] = p.Name
	}
	slots, planned := bl.g.callPlan(t, len(params))
	if !planned {
		panic("irbuild: a sibling call placed for another callee than the one the checker resolved")
	}
	temps := make([]ir.Temp, len(params))
	mobile := make([]bool, len(params))
	for i := range temps {
		temps[i] = ir.NoTemp
	}
	for _, i := range slots.order {
		a, slot := argExpr(t.Args[i]), slots.slots[i]
		src, k, held, ok := bl.lowerTypedOperand(a, params[slot])
		if !ok {
			return no()
		}
		// Only the final operand may stay unforced; inside an assertion
		// subject it is named twice, in its row and in the call.
		if src, k, ok = bl.convertTo(a, src, k, params[slot]); !ok {
			return no()
		}
		temps[slot], mobile[slot] = src, held
	}
	plan := bl.g.siblingFuncPlan(site.unit, f, result)
	if sym != nil {
		// A generic declaration's instance, interned in the declaring unit.
		plan.sym = sym
	}
	owner := bl.g.unitGen(site.unit)
	accessor := func(slot int) *ir.Symbol { return owner.irFileSlotSym(plan.sym, slot) }
	if !bl.fillCallDefaults(t, params, temps, mobile, defaultHome{owner: owner, accessor: accessor}) {
		return no()
	}
	if t != bl.pipedCall {
		bl.recordCallSlots(t.Args, slots, names, temps, params, result)
	}
	return bl.qualEmit(t, irQualArgs{temps: temps, kinds: params, ok: true}, plan)
}

// irSiblingCalleeSym is the callee symbol the DECLARING unit uses for one
// sibling file's `fn`, interned in that unit's own table.
//
// THE SYMBOL BELONGS TO THE DECLARATION AND THE TABLE BELONGS TO THE UNIT, and
// this is the one call in the package that crosses the second to honour the
// first. `irFuncShellFor` in unit `to` interns `Symbol(sig.decl, fd.Name)`;
// this interns `Symbol(f.decl, f.name)` in the SAME table with `f.decl ==
// sig.decl` and `f.name == fd.Name`, so `ir.Table.Symbol`'s "a repeat call
// returns the first entry" makes the two one pointer whichever unit is lowered
// first. Offering the declaration's own bare name rather than
// `unit.key + "." + f.name` is what makes that order-independent: the first
// caller sets the printed name, so two different strings would have made a
// retained function's `Name()` depend on whether some other file called it.
//
// NIL WHEN THERE IS NO PROGRAM TO LINK: no registry, so no sibling gens, so
// the caller interns in its own table. `qualSiblingPlan` is unreachable there
// anyway — `g.files` is nil — and the guard is the fence rather than the
// mechanism.
//
// SEQUENTIAL BY CONSTRUCTION. `ir.Table` needs no lock because it is one
// lowering's, and `Generate` lowers units in order on one goroutine; this adds
// no second writer that a lock would have to cover.
func (g *gen) irSiblingCalleeSym(to int, f *fileFunc) *ir.Symbol {
	if f.decl == nil || g.reg == nil || to < 0 || to >= len(g.reg.gens) {
		return nil
	}
	owner := g.reg.gens[to]
	if owner == nil {
		return nil
	}
	return owner.irCalleeSym(f.decl, f.name)
}

// qualSignature is the domain question: the arity, the result kind and every
// operand kind. An operand whose value enters an existential parameter by
// erasure (irErases) is that parameter's value: the VM's existential is the
// concrete value itself, so it is passed unchanged.
//
// It asks `bl.call`'s own two predicates and adds none: `irCallableValueKind`
// for the result, with `kindUnit` beside it because a Unit-returning callee
// is a discarded call, and `irCallOperandKind` for each operand.
func (bl *irScalarBuilder) qualSignature(t *ast.Call, args irQualArgs, params []kind, result kind) bool {
	if !args.ok || len(params) != len(t.Args) {
		return false
	}
	if !irCallableValueKind(result) && result != kindUnit {
		return false
	}
	for i, k := range args.kinds {
		if !irCallOperandKind(k) {
			return false
		}
		// A value at wider type arguments it already has passes as it is
		// (irrelabel.go), as an erased one does.
		if k != params[i] && !bl.g.irErases(params[i], k, nil) && (irHeldKind(params[i]) || !bl.passesRelabelled(t.Args[i], k, params[i])) {
			return false
		}
	}
	return true
}

// qualEmit appends the call.
//
// Arguments are already materialized in evaluation order by irQualLowerArgs.
func (bl *irScalarBuilder) qualEmit(t *ast.Call, args irQualArgs, p *irQualPlan) (ir.Temp, kind, bool, bool) {
	// THE CALLEE'S IDENTITY IS THE PLAN'S WHEN THE PLAN HAS ONE, which is the
	// cross-unit case: `irSiblingCalleeSym` already interned it in the
	// declaring unit's table, so the `ir.Call` here and the `ir.Func` there
	// name ONE symbol and `ir.Module.FuncFor` can answer across the pair.
	callee := p.sym
	if callee == nil {
		callee = bl.g.irCalleeSym(p.token, p.name)
	}
	if p.dispatch || p.bound {
		// Neither names an implementation, so the record must name none.
		bl.g.verifyDispatched(t)
	} else if target, block := irQualPlanTarget(p); target != nil {
		bl.g.verifyCalleeIn(t, target, block)
	}
	bl.g.verifyCalleeAt(t, p)
	if decl := irQualPlanDecl(p); decl != nil {
		bl.g.irNoteBootCall(decl, callee)
		// `fn x -> lib.keep(x)` over another file's iter-sensitive keep.
		signalling, ok := bl.iterSensitiveCall(t, decl, p.name)
		if !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
		if signalling {
			if p.host || p.dispatch {
				bl.g.dc.note("a host or dispatched call to a function that uses break or continue: " + p.name)
				return ir.NoTemp, kindInvalid, false, false
			}
			if p.args != nil {
				args = *p.args
			}
			return bl.signallingCall(t, decl, callee, args.temps, p.result)
		}
	}
	constructor := ir.NewCall
	if p.host {
		constructor = ir.NewHostCall
	}
	if p.dispatch {
		constructor = func(pos ir.Pos, dst ir.Temp, site ir.CallSite, callee *ir.Symbol, args ...ir.Temp) *ir.Call {
			return ir.NewDispatchCall(pos, dst, site, callee, p.dispatchAt, args...)
		}
	}
	if p.bound {
		constructor = ir.NewBoundCall
	}
	if p.args != nil {
		args = *p.args
	}
	c := constructor(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
		callee, args.temps...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: p.result, deferrable: true})
	return c.Dst(), p.result, false, true
}

// irQualPlanDecl is the Nomi function declaration a qualified call's plan
// calls, or nil: a program's, or a std module's Nomi body (a std module's
// own test naming its iter-sensitive helper as an Iter.loop step).
func irQualPlanDecl(p *irQualPlan) *ast.FuncDef {
	switch tok := p.token.(type) {
	case *fileFunc:
		return tok.decl
	case *stdFunc:
		return tok.decl
	case *fnSig:
		return tok.decl
	}
	return nil
}

// irQualPlanTarget is the declaration a qualified call's plan lowers
// against, which verifyCallee holds to the call's record: a program's or a
// std module's function, an impl's function, or for an impl that inherits
// the interface's default, that interface function with the block that
// inherits it; a `host fn`; an interface function a plan calls for one
// receiver. Nil for a plan that dispatches where it runs.
func irQualPlanTarget(p *irQualPlan) (ast.Node, *ast.ImplBlock) {
	if p.target != nil {
		return p.target, nil
	}
	var n ast.Node
	switch tok := p.token.(type) {
	case *fileFunc:
		n = tok.decl
	case *stdFunc:
		n = tok.node
	case *fnSig:
		n = tok.decl
	case *implItem:
		return tok.source()
	case irHostBinding:
		n = tok.ef
	case *ifaceMethod:
		if !p.dispatch {
			n = tok.decl
		}
	}
	if isNilNode(n) {
		return nil, nil
	}
	return n, nil
}

// irStdDisplayExistential is std Display's existential (`List<Display>`'s
// element, `Fragment<Display>`'s payload). Display is a prelude interface no
// program may redeclare, so the name is its identity.
func irStdDisplayExistential(k kind) bool {
	return irExistentialKind(k) && k.iface.nomi == "Display"
}

// displayCall lowers `Display.to_string(x)`, and a scalar's `to_string`
// (`Int.to_string(n)`), as the form of the call's record says, where that
// form decides it, and answers handled false where it does not: by an
// impl machineImpls renders (std's Display of Int, Float, Bool, True,
// False, String and Decimal), the machine's rendering of the value, as an
// interpolation hole of its type lowers it (a String is its own text,
// any other the machine's rendering); by dispatch, the value std's
// Display holds, rendered through its own impl; by shape (a tuple, an
// anonymous record), as its interpolation renders. Any other impl is
// called, by the routes after: a std container's instance
// (ifaceContainerCall) or the function the record names.
func (bl *irScalarBuilder) displayCall(t *ast.Call, args irQualArgs) (v ir.Temp, ok, handled bool) {
	if !args.ok || len(args.kinds) != 1 {
		return ir.NoTemp, false, false
	}
	k, src, at := args.kinds[0], args.temps[0], t.Args[0]
	c := bl.g.calleeAt(t)
	switch c.Form {
	case analysis.OperatorByImpl:
		kt := bl.g.checkerTypeArg(c.For)
		if bl.g.machineRouteOf(operatorUseOfCall(stdDisplayType, c, kt)) != routeStructural {
			return ir.NoTemp, false, false
		}
		if k != kindString && !irScalarLeafKind(k) && !isDecimalKind(k) {
			// True's or False's marker, as Bool's derived body binds a
			// variant's payload: no Bool the machine holds, so the
			// routes after call its body, as an equality over it
			// compares the marker by shape.
			return ir.NoTemp, false, false
		}
		rec := &analysis.DisplayRender{Form: analysis.DisplayScalar}
		if analysis.ResolveTypeVar(kt) == analysis.TypeString {
			rec.Form = analysis.DisplayText
		}
		v, ok := bl.renderDisplay(at, src, k, rec, nil)
		return v, ok, true
	case analysis.OperatorDispatched:
		v, ok := bl.renderDisplay(at, src, k, &analysis.DisplayRender{Form: analysis.DisplayInterface}, nil)
		return v, ok, true
	case analysis.OperatorStructural:
		v, ok := bl.displayValue(at, src, k, bl.g.firstOperandType(t))
		return v, ok, true
	}
	return ir.NoTemp, false, false
}

// inspectCall lowers `Debug.inspect(x)` as its record says (debugRender),
// and answers the route that decided it, "" where it declines. Where the
// record names this unit's own impl function (a struct's, an enum's, an
// opaque or composite distinct's, written, derived or universal), it
// declines, and the call is planned from the function the record names,
// as any recorded call is (qualImplPlan's recordedCallPlan). What the call
// is qualified with is the record's (Callee.RouteOwner), whatever it is
// spelled, and so is the function it names (Callee.Owner): `Point.inspect(p)`
// for a type with no Debug block names Debug's own.
func (bl *irScalarBuilder) inspectCall(t *ast.Call, args irQualArgs, method string) (ir.Temp, kind, bool, callRoute) {
	no := func() (ir.Temp, kind, bool, callRoute) { return ir.NoTemp, kindInvalid, false, "" }
	if method != "inspect" || !args.ok || len(args.kinds) != 1 {
		return no()
	}
	k := args.kinds[0]
	rc := bl.g.routeCallee(t)
	if owner := rc.RouteOwner().Std; owner != stdDebug && rc.Owner.Std != stdDebug {
		// `Vector.inspect(v)`: the container's own Debug impl named by its
		// type is `Debug.inspect(v)`, whose nominal elements render through
		// their own impls; std has one body for both.
		if base := irContainerBaseName(k); base == "" || base != owner.Name || base == "Range" || bl.g.ownOwner(t) {
			return no()
		}
	}
	c := bl.g.calleeAt(t)
	kt := bl.g.checkerTypeArg(c.For)
	if c.Form == analysis.OperatorByImpl && !bl.g.debugByMachine(c, kt) {
		if og, it := bl.g.recordedImplItem(c.Decl, nil, k); it != nil && og == bl.g {
			return no()
		}
	}
	v, by := bl.debugRender(t, irCallSite(t), args.temps[0], k, kt, c)
	if by == "" {
		return no()
	}
	return v, kindString, false, by
}

// qualCalleeIsValue reports whether fa, the qualified callee of the call
// t, is a value read by a field chain rather than an owner qualifier: a
// bound local's field (`gen.run(seed)`), or a chain of fields and tuple
// indices, of any length, that the call's record says qualifies no type,
// interface or type parameter (`p.t.first(8)`, `make().q.f(1)`), where a
// chain that does (`json.Json.parse(s)`, `boxes.Shape.twice(3)`) is the
// owner (qualCallLowered). Such a call goes through the function value the
// chain reads.
func (bl *irScalarBuilder) qualCalleeIsValue(t *ast.Call, fa *ast.FieldAccess) bool {
	if fa.Field == nil {
		return false
	}
	if _, app := bl.g.appRead(fa); app {
		// `App.steps(10)`: an application field of function type, read as
		// any field read is and called through the value it holds.
		return true
	}
	switch obj := fa.Object.(type) {
	case *ast.Ident:
		// A value the body binds, or a `once` (`tool.run(2)` over `once
		// tool = Runner{run: f}`): both are values whose field is read.
		if ref := bl.g.identRef(obj); ref != nil && ref.Kind == analysis.SymbolOnce {
			return true
		}
		return bl.namesLocal(obj)
	case *ast.TypeIdent:
		return false
	}
	switch bl.g.routeCallee(t).RouteOwner().Kind {
	case analysis.QualType, analysis.QualInterface, analysis.QualTypeParam:
		return false
	}
	return true
}

// stdOwnerFuncRef reports whether t, a dotted name used as a value
// (`lists.List.first`), names a function of a std type: the checker's
// reference at its last name is a std function with an owner.
func (g *gen) stdOwnerFuncRef(t *ast.FieldAccess) bool {
	if t.Field == nil || g.fa == nil {
		return false
	}
	s := resolveSymbol(g.identRef(t.Field))
	if s == nil {
		return false
	}
	id := g.fa.StdFuncOf(s.Node)
	return !id.IsZero() && id.Owner != ""
}

// stdInterfaceDecl is the declaration of std interface iface, or nil when
// its module declares no interface of that name.
func stdInterfaceDecl(fa *analysis.FileAnalysis, iface analysis.StdType) *ast.InterfaceDef {
	sym := stdDeclaringSymbol(fa, iface.Module, iface.Name)
	if sym == nil || sym.Kind != analysis.SymbolInterface {
		return nil
	}
	decl, _ := sym.Node.(*ast.InterfaceDef)
	return decl
}

// recordedStdIface is the std interface the record of t, a call or a call
// the builder wrote (stdIfaceCallRecord), says it is qualified with, when
// it names one. A call qualified by a type parameter (`T.from_json(json)`)
// goes through its bound's interface, the owner of the function its
// record names.
func (g *gen) recordedStdIface(t *ast.Call) (analysis.StdType, bool) {
	c := g.routeCallee(t)
	q := c.RouteOwner()
	if q.Kind == analysis.QualTypeParam {
		q = c.Owner
	}
	return q.Std, q.Kind == analysis.QualInterface && !q.Std.IsZero()
}

// recordedIface is this gen's def of the interface the record of t says
// the call is qualified with (analysis.Callee.RouteOwner), found by its
// declaration (ifaceOfDecl): a program's interface, this file's own or a
// mirror of another file's however the call spells it (a dotted owner, an
// alias), and a std interface where this gen lowers the std module that
// declares it. Nil for a call qualified otherwise, and for a std interface
// this gen does not declare, whose impls the routes after find by its key
// (recordedStdIface).
func (g *gen) recordedIface(t *ast.Call) *ifaceDef {
	return g.ifaceOfQualifier(g.routeCallee(t).RouteOwner())
}

// ifaceOfQualifier is this gen's def of the interface q names, found by its
// declaration as recordedIface finds one; nil when q names no interface or
// one this gen has no def of.
func (g *gen) ifaceOfQualifier(q analysis.Qualifier) *ifaceDef {
	if q.Kind != analysis.QualInterface {
		return nil
	}
	if q.Decl != nil {
		return g.ifaceOfDecl(q.Decl)
	}
	if q.Std.IsZero() {
		return nil
	}
	if decl := stdInterfaceDecl(g.fa, q.Std); decl != nil {
		return g.ifaceOfDecl(decl)
	}
	return nil
}

// qualWidenEmbeds widens each operand of an embedded type whose parameter is
// the enum that embeds it into its `embeds` variant, and views each source
// whose parameter is an `Iter<T>` as that sequence (`Statement.join(parts,
// sep)` over a list), as a direct call's operand is (convertTo). changed
// is false when no operand needed it, and args is then unchanged.
// qualEraseSlotted is args with each operand whose slot is interface-typed,
// and whose own kind is a concrete one that erases into it, entered at the
// slot's kind: `Worker.new(Inline.new(), 2)` against `queue: Queue`, which
// leaves defaults out, so an operand's slot is read off the call's slot
// plan rather than its position.
func (bl *irScalarBuilder) qualEraseSlotted(t *ast.Call, args irQualArgs, params []kind, decl []ast.Param) (irQualArgs, bool) {
	if !args.ok || len(args.temps) != len(t.Args) || len(decl) != len(params) {
		return args, false
	}
	plan, planned := bl.g.callPlan(t, len(params))
	if !planned {
		return args, false
	}
	var out irQualArgs
	for i, slot := range plan.slots {
		if slot < 0 || args.kinds[i] == params[slot] || !irHeldKind(params[slot]) {
			continue
		}
		v, got, ok := bl.convertTo(argExpr(t.Args[i]), args.temps[i], args.kinds[i], params[slot])
		if !ok {
			return args, false
		}
		if out.temps == nil {
			out = irQualArgs{temps: slices.Clone(args.temps), kinds: slices.Clone(args.kinds), mobile: slices.Clone(args.mobile), ok: true}
		}
		out.temps[i], out.kinds[i] = v, got
	}
	if out.temps == nil {
		return args, false
	}
	return out, true
}

func (bl *irScalarBuilder) qualWidenEmbeds(t *ast.Call, args irQualArgs, params []kind) (irQualArgs, bool) {
	if !args.ok || len(args.kinds) != len(params) {
		return args, false
	}
	var out irQualArgs
	for i, k := range args.kinds {
		seq := params[i].tag == tagSeq && params[i].comp != nil
		// A concrete value entering an interface-typed parameter
		// (`Worker.new(Inline.new(), 2)` against `queue: Queue`) enters by
		// erasure, as it does a module function's parameter.
		erased := irHeldKind(params[i])
		if k == params[i] || (!irEmbeddedIn(params[i], k) && !seq && !erased) {
			continue
		}
		if out.temps == nil {
			out = irQualArgs{temps: slices.Clone(args.temps), kinds: slices.Clone(args.kinds), mobile: slices.Clone(args.mobile), ok: true}
		}
		var at ast.Node = t
		if i < len(t.Args) && !isNilNode(t.Args[i]) {
			at = t.Args[i]
		}
		v, got, ok := bl.convertTo(at, args.temps[i], k, params[i])
		if !ok {
			return args, false
		}
		out.temps[i], out.kinds[i] = v, got
		if i < len(out.mobile) && v != args.temps[i] {
			// The widened value is a pure construction (embedWiden); an
			// erased one is the operand itself and keeps its own.
			out.mobile[i] = true
		}
	}
	if out.temps == nil {
		return args, false
	}
	return out, true
}
