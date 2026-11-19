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
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// irQualPlan is one resolved qualified callee: an identity, a result kind, and
// how qualEmit appends the call.
//
// A PLAN RATHER THAN AN IMMEDIATE BUILD, because the operands are lowered
// BEFORE the callee is selected — `stdPick` chooses on the operand kinds — and
// a plan keeps "what did we resolve" separate from "what do we append".
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
// `stdPick` resolves an overload set on the operand kinds, and `byIface`
// needs the RECEIVER's kind to find the implementation at all. So this
// producer cannot ask "is the callee resolvable" before asking "what do the
// operands lower to", which is the reverse of `bl.call`'s bare-callee order.
type irQualArgs struct {
	temps  []ir.Temp
	kinds  []kind
	mobile []bool
	// ok is false when any operand did not lower.
	ok bool
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

// ifaceContainerCall lowers `Iface.method(x, ...)` whose receiver x is a std
// container or prelude enum (`Display.to_string(set)`,
// `Hashable.hash(Some(1))`) to std's `impl Iface for Owner<T>` member,
// instantiated at x's kind as `Owner.method(x, ...)` would be. The operands
// are the ones irQualLowerArgs already lowered.
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

func (bl *irScalarBuilder) ifaceContainerCall(t *ast.Call, args irQualArgs, iface, method string, selfType analysis.Type) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	// Inside a std module only an instance body or a test body instantiates,
	// as an owner-qualified `Set.f(...)` call in a test body does.
	if (bl.g.stdModule != "" && !bl.stdInstRangeReceiver(args) && !bl.stdTestOwnType()) || !args.ok || len(args.kinds) == 0 {
		return no()
	}
	if _, local := bl.g.types[iface]; local {
		return no()
	}
	if d, isIface := bl.g.ifaceNamed(iface); isIface && d != nil && bl.g.stdInstCur == nil && !bl.g.stdCases && !bl.g.namesStdIfaceBeside(t, d) {
		// A local interface is dispatched through its own table. A std
		// instance's module, or the module whose cases these are, may
		// declare the interface its container impl implements
		// (`ToJson.to_json(item)` in `List.to_json<List<Note>>`), and that
		// impl is the template checked below.
		return no()
	}
	self := args.kinds[0]
	if iface == "Display" && method == "to_string" && len(args.kinds) == 1 &&
		(irRetainedRecordKind(self) || irRetainedTupleKind(self)) && irStructuralDisplayKind(self) {
		// An anonymous record or a tuple has no impl: it renders
		// structurally, which is rt.DisplayText.
		r := ir.NewRenderDisplay(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), kindString, false, true, true
	}
	base := irContainerBaseName(self)
	if base == "" {
		return no()
	}
	if iface == "Contains" && method == "contains" {
		// `Contains.contains(set, x)`: each container's impl is the
		// intrinsic its type-qualified call reaches (`Set.contains`), so the
		// interface-qualified call is that call.
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
	f := bl.g.stdGenericTemplate(base, method)
	if f == nil || bl.g.stdInsts == nil {
		return no()
	}
	_, ib := bl.g.stdInsts.stdTemplateParams(f)
	if ib == nil || ib.Interface == nil || typeText(ib.Interface) != iface && !strings.HasPrefix(typeText(ib.Interface), iface+"<") {
		return no()
	}
	prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
	bl.stdInstPreCall, bl.stdInstPreArgs = t, args
	defer func() { bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs }()
	return bl.stdInstCallAt(t, f, self, selfType)
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
	if bl.qualCalleeIsValue(fa) {
		// `p.t.first(8)`, `gen.run(seed)`: a function read by a field chain,
		// called through the value it holds. An indirect call records its
		// own rows in an assertion subject.
		return bl.indirectCall(t)
	}
	if bl.recording > 0 {
		// Inside an assertion subject the call's rows are recorded after the
		// call (recordedQualCall).
		if ti, isType := fa.Object.(*ast.TypeIdent); isType && ti.Name == "Iter" && fa.Field != nil && fa.Field.Name == "loop" {
			// The callback is inlined and records nothing; the loop's
			// answer is the row its enclosing operand records.
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
		if _, isSubtype := bl.g.refinementDecl(ti.Name); isSubtype {
			// A subtype's `unchecked` is its value, which is its base's
			// (uncheckedRefinement): `ClosedRange.unchecked(r)` in a std
			// body the shared lowering retains, which builds no instance
			// of the generic `unchecked`.
			return bl.lower(t.Args[0])
		}
	}
	if isType {
		// What the checker resolved the qualifier to, and how this file
		// reaches it (qualOwner).
		switch route, name, mod := bl.qualOwner(t, ti); route {
		case ownerStdModule:
			// `Duration.seconds(1)` where this file binds `Duration` to
			// another declaration, or a subtype's refinement check in a
			// file that binds no name to it: the call
			// `duration.Duration.seconds(1)` is.
			if v, rk, mobile, ok, handled := bl.stdModuleOwnerCall(t, mod, name, method, false); handled {
				return v, rk, mobile, ok
			}
			return no()
		case ownerResolved:
			// A program's type this file does not reach by its name: the
			// owner function the checker resolved the call to.
			if bl.qualBare == nil {
				bl.qualBare = bl.checkedBareOperands(t)
			}
			args := bl.irQualLowerArgs(t)
			if plan := bl.resolvedOwnerPlan(t, args, fa); plan != nil {
				return bl.qualEmit(t, args, plan)
			}
			return no()
		default:
			if name != ti.Name {
				// `import std::iter.Iter as It`, then `It.count(xs)`: the
				// call is std's `Iter.count`, which every arm below names
				// by its own name.
				ti = &ast.TypeIdent{Name: name, Line: ti.Line, Col: ti.Col}
			}
		}
	}
	if !isIdent && !isType {
		// Shape B: a 3-segment qualifier. A namespaced type name
		// (`Json.DecodeError.to_string`, `shapes.Colour.of`) resolves as
		// that type's owner; any other chain is a value, which qualCall
		// sent to indirectCall (qualCalleeIsValue).
		owner, mod, ok := bl.qualDottedOwner(fa.Object)
		if !ok {
			if owner, ok = bl.siblingOwnerSpelling(fa.Object, method); !ok {
				return no()
			}
		}
		if mod != "" && bl.stdModuleInterface(mod, owner) {
			// `display.Display.to_string(x)`: a std interface named
			// through its module is the interface the bare `Display`
			// names, which the interface route dispatches. Where this
			// file declares a type or interface of that name, the bare
			// name is that declaration, so the call is resolved from the
			// interface's own.
			if !bl.g.stdOwnerUnshadowed(owner) {
				return bl.stdModuleIfaceCall(t, mod, owner, method)
			}
		} else if mod != "" {
			if v, rk, mobile, ok, handled := bl.stdModuleOwnerCall(t, mod, owner, method, bl.g.stdOwnerUnshadowed(owner)); handled {
				return v, rk, mobile, ok
			}
			// `lists.List.first(xs)`, `iter.Iter.find(xs, f)`: an operation
			// of a family the VM owns, which the two-segment spelling
			// reaches through its owner's own route. The bare owner names
			// the same std type here, so it is that call.
		}
		ti, isType = &ast.TypeIdent{Name: owner, Line: fa.Line, Col: fa.Col}, true
	}
	if isType {
		return bl.typeQualCall(t, ti, method)
	}
	bl.qualPreResolve(nil, obj, false, method)
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

// signallingOwner reports whether Owner.method drives its second operand as
// a signalling callback (irSignallingCallback), with `Map` std's.
func (bl *irScalarBuilder) signallingOwner(owner, method string) bool {
	if !irSignallingCallback(owner, method) {
		return false
	}
	if owner == "Map" {
		// A program's own `Map` type takes no signalling callback.
		if _, shadowed := bl.g.namedType("Map"); shadowed && bl.g.stdModule == "" {
			return false
		}
	}
	return true
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

// typeQualCall lowers `Owner.method(args)` for the type owner ti, which
// qualCallLowered resolved from the qualifier and the bare std-method route
// from the declaration the checker bound the callee to (bareStdMethodCall).
// Every fact it reads about the call is keyed on t, the call as written.
func (bl *irScalarBuilder) typeQualCall(t *ast.Call, ti *ast.TypeIdent, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	route, routeKey := bl.qualRoute(t, ti, method)
	signalOwner := len(t.Args) == 2 && bl.signallingOwner(ti.Name, method)
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
	bl.qualPreResolve(ti, nil, true, method)
	switch route {
	case routeContainerEqual:
		if len(t.Args) == 2 {
			bl.g.verifyCallForm(t, "containerEqualCall", analysis.OperatorByImpl)
			return bl.containerEqualCall(t, routeKey.Owner.Name)
		}
	case routeHash:
		if v, rk, mobile, ok, handled := bl.tupleHashCall(t); handled {
			bl.g.verifyCallForm(t, "tupleHashCall", analysis.OperatorStructural)
			return v, rk, mobile, ok
		}
	case routeToJson:
		// Only std's ToJson admits a record (irrecordjson.go).
		if v, rk, mobile, ok, handled := bl.recordToJsonCall(t, method); handled {
			bl.g.verifyCallForm(t, "recordToJsonCall", analysis.OperatorStructural)
			return v, rk, mobile, ok
		}
	case routeCompare:
		if v, rk, mobile, ok, handled := bl.tupleCompareCall(t); handled {
			bl.g.verifyCallForm(t, "tupleCompareCall", analysis.OperatorStructural)
			return v, rk, mobile, ok
		}
	case routeEquatableEqual:
		if len(t.Args) == 2 && bl.g.stdModule != "" {
			// `Equatable.equal(a.max_age, b.max_age)` in a std body (a
			// derived impl's field over a `Maybe<Int>`): a std module holds
			// no generic instance, and the container's equality is the
			// structural comparison containerEqualCall lowers.
			if owner := bl.equatableContainerOwner(t); owner != "" {
				bl.g.verifyCallForm(t, "containerEqualCall", analysis.OperatorByImpl)
				return bl.containerEqualCall(t, owner)
			}
		}
	case routePreludePredicate:
		if len(t.Args) == 1 {
			return bl.preludePredicateCall(t, routeKey)
		}
	}
	if k, bound := bl.g.genericSubstKind(ti.Name); bound {
		// `T.from_json(item)` inside an instance: T is the instance's
		// concrete argument, and the call is that type's.
		kt, _ := bl.g.genericSubstType(ti.Name)
		if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, kt, method); handled {
			return v, rk, mobile, ok
		}
		return no()
	}
	if v, rk, mobile, ok, handled := bl.stdGenericQualCall(t, ti.Name, method); handled {
		return v, rk, mobile, ok
	}
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	args := bl.irQualLowerArgs(t)
	var plan *irQualPlan
	if v, k, mobile, ok := bl.scalarOperatorCall(t, args, ti.Name, method); ok {
		bl.g.verifyCallForm(t, "scalarOperatorCall", analysis.OperatorByImpl)
		return v, k, mobile, true
	}
	if v, k, mobile, ok, handled := bl.scalarEqualCall(t, args, ti.Name, method); handled {
		bl.g.verifyCallForm(t, "scalarEqualCall", analysis.OperatorByImpl)
		return v, k, mobile, ok
	}
	switch route {
	case routeEquatableEqual:
		if v, ok, handled := bl.structuralEqualCall(t, args); handled {
			if args.ok && len(args.kinds) == 2 && irHeldKind(args.kinds[0]) && irHeldKind(args.kinds[1]) {
				bl.g.verifyCallForm(t, "structuralEqualCall", analysis.OperatorDispatched)
			} else {
				bl.g.verifyCallForm(t, "structuralEqualCall", analysis.OperatorStructural)
			}
			return v, kindBool, false, ok
		}
	case routeHash:
		if v, owned := bl.existentialHashCall(t, args); owned {
			bl.g.verifyCallForm(t, "existentialHashCall", analysis.OperatorDispatched)
			return v, kindInt, false, true
		}
		if v, owned := bl.structuralHashCall(t, args); owned {
			bl.g.verifyCallForm(t, "structuralHashCall", analysis.OperatorStructural, analysis.OperatorDispatched)
			return v, kindInt, false, true
		}
	case routeScalarText:
		bl.g.verifyCallForm(t, "scalarTextCall", analysis.OperatorByImpl)
		return bl.scalarTextCall(t, args, routeKey.Owner.Name)
	case routeDisplay:
		if args.ok && len(args.kinds) == 1 && args.mobile[0] && irDisplayScalar(args.kinds[0]) && bl.g.stdModule == "" {
			bl.g.verifyCallForm(t, "scalarTextCall", analysis.OperatorByImpl, analysis.OperatorDispatched)
			v, k, mobile, ok := bl.scalarTextCall(t, args, args.kinds[0].nomi())
			return v, k, mobile, ok
		}
		if args.ok && len(args.kinds) == 1 && irStdDisplayExistential(args.kinds[0]) {
			bl.g.verifyCallForm(t, "RenderDisplayErased", analysis.OperatorDispatched)
			// `Display.to_string(v)` over std Display's existential: the VM
			// renders the value it holds through that value's own impl.
			r := ir.NewRenderDisplayErased(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString})
			return r.Dst(), kindString, false, true
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
	if v, k, mobile, ok := bl.synthDebugRender(t, args, ti.Name, method); ok {
		return v, k, mobile, true
	}
	if v, k, mobile, ok, handled := bl.ifaceContainerCall(t, args, ti.Name, method, bl.g.firstOperandType(t)); handled {
		if route == routeDisplay && args.ok && len(args.kinds) == 1 && (irRetainedRecordKind(args.kinds[0]) || irRetainedTupleKind(args.kinds[0])) {
			bl.g.verifyCallForm(t, "ifaceContainerCall", analysis.OperatorStructural)
		} else {
			bl.g.verifyCallForm(t, "ifaceContainerCall", analysis.OperatorByImpl)
		}
		return v, k, mobile, ok
	}
	if route == routeDisplay && args.ok && len(args.kinds) == 1 {
		// The rest of `Display.to_string`'s cases, after the interface's
		// container impls (ifaceContainerCall).
		if irStructuralDisplayKind(args.kinds[0]) {
			// `Display.to_string(x)` over a value with no nominal part, as
			// std's own instances call it on an element
			// (`List.to_string<Bool>` renders each Bool this way): the text
			// is structural, which rt.DisplayText is.
			bl.g.verifyCallForm(t, "RenderDisplay", analysis.OperatorStructural, analysis.OperatorByImpl, analysis.OperatorDispatched)
			r := ir.NewRenderDisplay(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString})
			return r.Dst(), kindString, false, true
		}
		if irRetainedRecordKind(args.kinds[0]) || irRetainedTupleKind(args.kinds[0]) {
			// A tuple or record with a nominal part renders as its
			// interpolation does.
			if r, ok := bl.displayValue(t.Args[0], args.temps[0], args.kinds[0], nil); ok {
				bl.g.verifyCallForm(t, "displayValue", analysis.OperatorStructural)
				return r, kindString, false, true
			}
		}
	}
	if p := bl.stdRecvOwnerPlan(t, args, ti.Name, method); p != nil {
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
		plan = bl.qualImplPlan(t, args, ti.Name, method)
	}
	if plan == nil {
		plan = bl.siblingReceiverPlan(t, args, ti.Name, bl.g.qualOwnerKey(t, ti.Name), method)
	}
	if plan == nil {
		// `Int.min(3, 4)`, `Comparable.min(v, w)`: a std interface's
		// default that no impl of the receiver writes, instantiated in its
		// declaring module (stdifacedefault.go).
		if v, rk, mobile, ok, handled := bl.stdIfaceDefaultCall(t, args, ti.Name, method); handled {
			return v, rk, mobile, ok
		}
	}
	container := route == routeMap || route == routeSet || route == routeVector || route == routeList
	if plan == nil && container && (bl.g.stdModule == "" || bl.stdTestOwnType()) {
		// A Map, Set or Vector function with no intrinsic is std's Nomi body,
		// instantiated at the call's types like any generic std member.
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
		v, rk, mobile, ok, handled := bl.stdInstCallAt(t, bl.g.stdGenericTemplate(routeKey.Owner.Name, method), self, selfType)
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

// qualPreResolve resolves the qualifier before the arguments are lowered.
// Resolving a sibling file's interface or `fn` imports the kinds its signature
// names, and each import declares this package's Go alias for a sibling type
// in the order it happens, so the aliases are declared before any argument
// needs them.
func (bl *irScalarBuilder) qualPreResolve(ti *ast.TypeIdent, obj *ast.Ident, isType bool, method string) {
	g := bl.g
	if isType {
		g.ifaceNamed(ti.Name)
		return
	}
	if g.files == nil || bl.namesLocal(obj) {
		return
	}
	to, isSibling := g.lookupQualifier(obj)
	if !isSibling {
		return
	}
	f := g.files.units[to].funcs[method]
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
// interface function it names (dispatchedCallPlan), else by the owner's
// spelling and the operands' kinds, this module's own impl blocks first,
// then the stdlib index. The later routes serve a call the record leaves
// to dispatch whose receiver the builder holds as a value of a known type,
// a call with no record, and a recorded function recordedCallPlan does not
// reach (irbuild package doc, "Qualified calls from their records").
//
// A local receiver's implementation takes precedence over a stdlib type with
// the same spelling. The stdlib resolver independently rejects local shadows.
func (bl *irScalarBuilder) qualImplPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	key := owner + "." + method
	if p := bl.recordedCallPlan(t, args, key); p != nil {
		return p
	}
	if p := bl.dispatchedCallPlan(t, args); p != nil {
		// A receiver held as an interface value: dispatched on its
		// run-time type through the function the record names.
		return p
	}
	d, isIface := bl.g.ifaceNamed(owner)
	if isIface && d != nil && !bl.g.namesStdIfaceBeside(t, d) {
		return bl.concreteInterfacePlan(t, args, d, method)
	}
	// An interface this file holds no def of, or a std interface the
	// record names where this file's own interface takes its name: its
	// impls are the ones filed under its key.
	ifaceKey := bl.g.qualOwnerKey(t, owner)
	if p := bl.foreignIfacePlan(t, args, ifaceKey, method); p != nil {
		return p
	}
	k := bl.g.qualifierKind(owner)
	if tpl := bl.g.genericTemplates[owner]; tpl != nil {
		if inst, ok := bl.g.templateCallInstance(bl.g, t, tpl, method); ok {
			// `Span.new(1, 5)`, `Wrapped.describe(boxed)`, `Box.flat(b)`
			// over a `Box<Maybe<Int>>`: the instance of the block the
			// checker instantiated the call at, which need not be the
			// receiver operand's.
			k = inst
		} else if bl.instCall == t && bl.instKind.def != nil && bl.instKind.def.genericOf == tpl {
			// `FromJson.from_json<Field<String>>(json)`, as a derived
			// FromJson decodes a field, or `T.from_json(json)` with T bound
			// to that type: the instance the type names, which
			// stdKindQualCall carried beside the template's name.
			k = bl.instKind
		}
	}
	// kindInvalid: lookup — asks whether `owner` names a type at all, to choose between the local-impl path and falling through; a miss returns nil and the caller reports the qualified callee it could not resolve.
	if k != kindInvalid {
		var only *implItem
		for _, d := range bl.g.implsOf(k) {
			if d.recv != k || !d.lowerable {
				continue
			}
			if defs := bl.g.instImpls[d.ifaceKey][k]; len(defs) > 1 {
				// `Box.holds(b, 1)` where Box implements Holds at several
				// type arguments: one of them, the call's own.
				if d != defs[0] {
					continue
				}
				_, item := bl.pickInstImpl(t, defs, method)
				if item == nil {
					if defs[0].items[method] != nil {
						return nil
					}
					continue
				}
				if only != nil {
					return nil
				}
				only = item
				continue
			}
			if impl := bl.g.dispatchedImpl(t); impl != nil && d.instArgs != nil && !blockWrites(d.decl, impl) {
				// `String.contains(s, "x")` beside the program's
				// `impl Contains<Rate> for String`: the receiver's other
				// impls of the interface are std's, which the front end
				// chose and the stdlib route answers.
				continue
			}
			item := d.items[method]
			if item == nil {
				item = bl.genericMethodItem(t, d, method)
			}
			if item == nil {
				continue
			}
			if only != nil {
				// A second impl declares the method, so the call names
				// no one function. Declined.
				return nil
			}
			only = item
		}
		if only != nil {
			return bl.implItemPlan(t, args, only, key)
		}
	}
	if p := bl.qualSiblingImplPlan(t, args, owner, method); p != nil {
		return p
	}
	if args.ok && len(args.kinds) > 0 {
		if p := bl.callerImplPlan(t, args, ifaceKey, method, args.kinds[0]); p != nil {
			return p
		}
	}
	if p := bl.stdIfacePlan(t, args, owner, method); p != nil {
		return p
	}
	if p := bl.stdMethodCallPlan(t, args, owner, method); p != nil {
		return p
	}
	return bl.siblingReceiverPlan(t, args, owner, ifaceKey, method)
}

// qualOwnerKey is the key (ifaceKey) of the impls of the interface the
// qualified call t names as owner: this file's def owner reaches, unless
// t's record names a std interface that def is not; else the std
// interface t's record names; else the interface the checker binds owner
// to in this file's scope. "" when owner names no interface.
func (g *gen) qualOwnerKey(t *ast.Call, owner string) string {
	if d, isIface := g.ifaceNamed(owner); isIface && d != nil && !g.namesStdIfaceBeside(t, d) {
		return g.implsKey(d)
	}
	if std, named := g.recordedStdIface(t); named {
		return stdIfaceImplKey(std)
	}
	if it := g.headerIface(&ast.SimpleType{Name: owner}); it != nil {
		return ifaceKey(it.Origin, it.Name)
	}
	return ""
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

// siblingReceiverPlan resolves `Display.to_string(shapes.origin())`: an
// interface call whose receiver another file declares reaches that file's
// impl, written or synthesized, as `shapes.Point.to_string` would. The named
// interface's member first (the impls filed under key, its ifaceKey), so a
// method name two of the receiver's impls share (`Comparable.compare`,
// `Ranked.compare`) is the one the call names.
//
// Every route that plans an interface call goes through here, so `io.print(p)`
// and `"${p}"` reach the impl the written `Display.to_string(p)` reaches.
func (bl *irScalarBuilder) siblingReceiverPlan(t *ast.Call, args irQualArgs, owner, key, method string) *irQualPlan {
	// kindInvalid: lookup — the owner names no type, so it is an interface whose receiver another file may declare.
	if !args.ok || len(args.kinds) == 0 || bl.g.qualifierKind(owner) != kindInvalid {
		return nil
	}
	if _, undeclared := undeclaredRecvOwner(args.kinds[0]); undeclared && bl.g.stdModule == "" {
		// `Contains.contains(s, rate)`: a program's impl of a std
		// interface for a receiver no file declares, which the orphan
		// rule admits through a type argument the program declares
		// (`impl Contains<Rate> for String`), in whichever file writes it.
		_, item := bl.stdRecvCallee(t)
		return bl.stdRecvPick(t, args, bl.g.stdRecvCandidates(args.kinds[0], bl.g.firstOperandType(t), method, owner, item), method)
	}
	if args.kinds[0].tag != tagNamed || args.kinds[0].def == nil {
		return nil
	}
	if key != "" {
		if p := bl.qualSiblingIfaceImplPlan(t, args, args.kinds[0].def, method, key); p != nil {
			return p
		}
	}
	return bl.qualSiblingImplPlanFor(t, args, args.kinds[0].def, method)
}

// qualSiblingImplPlan resolves `Owner.method(args)` to an impl function a
// SIBLING FILE declares for Owner, tried after the local impl route: the one
// written site outside this unit, with the declaring file's kinds translated
// into this one's before the signature is compared. Anything
// that would need a refusal or a coercion declines.
func (bl *irScalarBuilder) qualSiblingImplPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	g := bl.g
	if g.files == nil || g.fileUnit < 0 || g.reg == nil {
		return nil
	}
	d, found := g.namedType(owner)
	if !found {
		return nil
	}
	return bl.qualSiblingImplPlanFor(t, args, d, method)
}

// qualSiblingImplPlanFor is qualSiblingImplPlan for a type already resolved,
// which a kind names even where no import brings its name into scope.
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
	p.sym = owning.irCalleeSym(chosen.item, chosen.symName)
	return p
}

// siblingImplSite is the one indexed impl function another file declares for
// its type d under `method`, narrowed to the impls of the interface whose
// key is iface (ifaceKey) when iface is not empty, or nil when there is
// none or more than one.
func (g *gen) siblingImplSite(d *typeDef, method, iface string) *implMemberSite {
	if d == nil {
		return nil
	}
	return g.siblingImplSiteBy(d.decl, method, iface, nil)
}

// siblingImplSiteBy is siblingImplSite for the receiver declared by recv,
// where several sites of one interface are its impls at several type
// arguments, and pick, when not nil, chooses the call's (instimpl.go). The
// sites are keyed by the receiver's declaration, so every spelling of the
// receiver (`Box`, `lib.Box`) finds the same ones.
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
		return bl.qualSiblingPlan(t, args, to, method)
	}
	// Stdlib file APIs have separate generic, testing and host-call routes.
	// They are outside the concrete type-qualified method path, except the
	// hosts with VM adapters (RtFuncs rows, with or without the frame).
	std, isStd := bl.g.stdFileQualifier(ownerID)
	if !isStd {
		std, isStd = bl.stdOwnFileQualifier(ownerID)
	}
	if !isStd {
		return bl.ownSiblingPlan(t, args, ownerID)
	}
	return bl.stdFilePlan(t, args, std, method)
}

// stdFilePlan plans a call to std file std's top-level function method,
// written `io.read_line()` or bare after `import std::io.read_line`.
func (bl *irScalarBuilder) stdFilePlan(t *ast.Call, args irQualArgs, std, method string) *irQualPlan {
	if bl.g.std == nil {
		return nil
	}
	// A host function by whichever row answers it (an rt function, with or
	// without the frame, a Go-backed adapter, a std/compiler crossing), or a
	// Nomi body (`json.shape_error_root`): the std module's cached function,
	// as derive synthesis calls it.
	return bl.stdFuncPlan(t, args, bl.g.std.byFile[std+"."+method])
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
func (bl *irScalarBuilder) qualSiblingPlan(t *ast.Call, args irQualArgs, to int, method string) *irQualPlan {
	unit := bl.g.files.units[to]
	f := unit.funcs[method]
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
	if site.host && !irHostFnHasVMBody(params, result) {
		// irHostFnRetain builds no VM body for a Go-bound declaration that
		// takes or returns a function, so the call would name a declaration
		// no module holds.
		bl.g.dc.note("a `go`-bound sibling declaration with no VM body")
		return no()
	}
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
	accessor := func(slot int) *ir.Symbol { return owner.irFileSlotSym(plan.sym, f.decl.Name, slot) }
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
		if k != params[i] && !bl.g.irErases(params[i], k) && (irHeldKind(params[i]) || !bl.passesRelabelled(t.Args[i], k, params[i])) {
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
	if p.dispatch {
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

// irDisplayScalar is a kind whose `Display.to_string` is the scalar renderer
// used for `Int.to_string` and its siblings.
// irStdDisplayExistential is std Display's existential (`List<Display>`'s
// element, `Fragment<Display>`'s payload). Display is a prelude interface no
// program may redeclare, so the name is its identity.
func irStdDisplayExistential(k kind) bool {
	return irExistentialKind(k) && k.iface.nomi == "Display"
}

func irDisplayScalar(k kind) bool {
	return k == kindInt || k == kindFloat || k == kindBool || k == kindString
}

// Concrete scalar Display calls consume the same Render node as interpolation.
func (bl *irScalarBuilder) scalarTextCall(t *ast.Call, args irQualArgs, owner string) (ir.Temp, kind, bool, bool) {
	if !args.ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if len(args.kinds) != 1 || !irScalarLeafKind(args.kinds[0]) || args.kinds[0] != bl.g.qualifierKind(owner) {
		// The checker types the one operand of `Int.to_string` as an Int,
		// and the Display route names the operand's own kind.
		panic("irbuild: a scalar's to_string over an operand that is not that scalar: " + owner)
	}
	r := ir.NewRenderDisplay(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst(), kindString, false, true
}

// synthDebugRender lowers `Debug.inspect(x)` to the Debug renderer `dbg`
// uses, for a value with no local Debug impl or a marker whose Debug is the
// synthesized default. A retained struct, enum or marker with its own impl
// calls that impl; that is how a nested field's Debug and an enum's derived
// Debug over an embedded value delegate.
func (bl *irScalarBuilder) synthDebugRender(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if method != "inspect" || !args.ok || len(args.kinds) != 1 {
		return no()
	}
	k := args.kinds[0]
	if owner != "Debug" {
		// `Vector.inspect(v)`: the container's own Debug impl named by its
		// type is `Debug.inspect(v)`, whose nominal elements render through
		// their own impls below; std has one body for both.
		if base := irContainerBaseName(k); base == "" || base != owner || base == "Range" {
			return no()
		}
		if _, local := bl.g.types[owner]; local {
			return no()
		}
	}
	if k.tag == tagFunc && bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k] == nil {
		// `Debug.inspect` of any function value is a name-free constant
		// (rt.FunctionInspectText). The operand was evaluated.
		bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorStructural)
		c := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), rt.FunctionInspectText())
		bl.b.Append(c)
		return c.Dst(), kindString, false, true
	}
	if k.tag == tagNamed && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def) || irRetainedMarker(k.def)) && bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k] != nil {
		plan, structural := bl.distinctDebugPlan(k)
		if plan != nil {
			bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorByImpl, analysis.OperatorStructural, analysis.OperatorDispatched)
			return bl.qualEmit(t, args, plan)
		}
		if !structural {
			return no()
		}
		bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorByImpl, analysis.OperatorStructural, analysis.OperatorDispatched)
	} else if irExistentialKind(k) || irConjKind(k) {
		// A local interface's existential holds its concrete value in the
		// VM: the renderer is told every implementer's Debug body.
		impls, ok := bl.existentialDebugImpls(k)
		if !ok {
			return no()
		}
		bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorDispatched)
		r := ir.NewRenderDebugWith(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0], impls)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), kindString, false, true
	} else if bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k] != nil {
		return no()
	} else if !irStructuralDebugKind(k) {
		// A container of nominal values, such as a std struct's list of
		// rows: the renderer names each nested type's Debug body.
		impls, ok := bl.nestedDebugImpls(k)
		if !ok {
			return no()
		}
		bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorByImpl, analysis.OperatorStructural, analysis.OperatorDispatched)
		// An `Iter<T>` whose possible sources name no impl renders
		// structurally, as the plain render below does.
		var r *ir.Render
		if len(impls) > 0 {
			r = ir.NewRenderDebugWith(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0], impls)
		} else {
			r = ir.NewRenderDebug(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0])
		}
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), kindString, false, true
	}
	bl.g.verifyCallForm(t, "synthDebugRender", analysis.OperatorByImpl, analysis.OperatorStructural, analysis.OperatorDispatched)
	r := ir.NewRenderDebug(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0])
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst(), kindString, false, true
}

// qualCalleeIsValue reports whether a qualified callee is a value read by a
// field chain rather than an owner qualifier: a bound local's field
// (`gen.run(seed)`), or a chain of fields and tuple indices, of any length,
// that names no namespaced type (`p.t.first(8)`, `make().q.f(1)`). Such a call
// goes through the function value the chain reads.
func (bl *irScalarBuilder) qualCalleeIsValue(fa *ast.FieldAccess) bool {
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
	if _, sibling := bl.siblingOwnerSpelling(fa.Object, fa.Field.Name); sibling {
		return false
	}
	_, _, owner := bl.qualDottedOwner(fa.Object)
	return !owner
}

// qualDottedOwner resolves a 3-segment qualifier's owner. A namespaced type
// name (`Json.DecodeError`, `shapes.Colour`) answers its whole spelling, which
// the type-qualified route resolves like any other owner, and so does an
// interface a program module or a std module lowered with the program
// declares (`shapes.Shape`, `storage.Store`). A stdlib file
// qualifier followed by a type (`random.Seed`) answers the bare type name and
// the module, because stdlib types are indexed by bare name and a local type
// of the same name must not capture the call.
func (bl *irScalarBuilder) qualDottedOwner(n ast.Node) (owner, stdModule string, ok bool) {
	inner, isFA := n.(*ast.FieldAccess)
	if !isFA || inner.Field == nil {
		return "", "", false
	}
	if name, dotted := bl.dottedTypeQualifier(n); dotted {
		return name, "", true
	}
	mod, isIdent := ast.QualifierIdent(inner.Object)
	if !isIdent || bl.namesLocal(mod) {
		return "", "", false
	}
	if _, isIface := bl.g.moduleQualifiedIfaceNamed(mod.Name, inner.Field.Name); isIface {
		// `shapes.Shape.area(s)`, `storage.Store.exists(s, "x")`: an
		// interface a program module or a std module lowered with the
		// program declares, which the type-qualified route dispatches as
		// it does the bare imported name.
		return mod.Name + "." + inner.Field.Name, "", true
	}
	std, isStd := bl.g.stdFileQualifier(mod)
	if !isStd {
		return "", "", false
	}
	return inner.Field.Name, std, true
}

// stdModuleInterface reports whether std module mod declares an interface
// named name.
func (bl *irScalarBuilder) stdModuleInterface(mod, name string) bool {
	sym := stdDeclaringSymbol(bl.g.fa, "std/"+mod, name)
	return sym != nil && sym.Kind == analysis.SymbolInterface
}

// stdModuleIfaceCall lowers `mod.Iface.method(args)`, a function of std
// module mod's interface Iface, in a file whose own type, template or
// interface takes the name Iface (`calendar.DateParts.year(d)` beside a
// `struct DateParts`). The bare spelling's route reads the owner through
// this file's tables, where the name is that declaration, so this call is
// resolved from the interface's declaration instead: the program's impl of
// that interface the call selects, else the module's own impl for a std
// receiver.
func (bl *irScalarBuilder) stdModuleIfaceCall(t *ast.Call, mod, iface, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	decl := stdDeclaringSymbol(bl.g.fa, "std/"+mod, iface)
	if decl == nil || decl.Kind != analysis.SymbolInterface || decl.Node == nil {
		panic("irbuild: stdModuleIfaceCall: stdModuleInterface found the same declaration in the same module before the call")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	if len(args.kinds) == 0 {
		// Every function of an interface takes its receiver, and the
		// checker holds a call to the function's arity.
		panic("irbuild: a call to a std interface's function with no operand: " + mod + "." + iface + "." + method)
	}
	if plan := bl.dispatchedCallPlan(t, args); plan != nil {
		// A receiver held as the std interface the module names:
		// dispatched through the function the record names.
		return bl.qualEmit(t, args, plan)
	}
	// A program impl of the std interface, filed under its key whatever
	// this file declares under the name.
	plan := bl.ifaceImplPlanOf(t, args, stdIfaceImplKey(analysis.StdType{Module: "std/" + mod, Name: iface}), method)
	if plan == nil {
		plan = bl.stdModuleIfacePlan(t, args, mod, iface, method)
	}
	if plan == nil {
		// A default no impl of the receiver writes
		// (`json.ToJson.to_json_field(s)`): the interface's own, which the
		// call's record names whatever this file binds the name to.
		if v, rk, mobile, ok, handled := bl.stdIfaceDefaultCall(t, args, iface, method); handled {
			return v, rk, mobile, ok
		}
		return no()
	}
	return bl.qualEmit(t, args, plan)
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

// namesStdIfaceBeside reports whether t's record names a std interface
// that d, the interface t's spelling reaches in this file (ifaceNamed), is
// not: a call the builder wrote of std's `ToJson.to_json_field` in a file
// that declares its own `interface ToJson`. The call is then std's, and d
// is not its interface. A std module's own gen holds its interface
// (std/json's ToJson) as a declaration of its own, which is the one the
// record names.
func (g *gen) namesStdIfaceBeside(t *ast.Call, d *ifaceDef) bool {
	std, named := g.recordedStdIface(t)
	if !named || d == nil || d.stdShared || d.stdOrigin != "" {
		return false
	}
	decl := stdInterfaceDecl(g.fa, std)
	return decl != nil && d.decl != decl
}

// ifaceImplPlanOf plans the call of a program's impl of the interface
// whose key is key (ifaceKey): this file's impl the call's signature
// selects (foreignIfacePlan), or another file's for the receiver operand.
func (bl *irScalarBuilder) ifaceImplPlanOf(t *ast.Call, args irQualArgs, key, method string) *irQualPlan {
	if p := bl.foreignIfacePlan(t, args, key, method); p != nil {
		return p
	}
	recv := args.kinds[0]
	if recv.tag != tagNamed || recv.def == nil || bl.g.siblingImplSite(recv.def, method, key) == nil {
		return nil
	}
	return bl.qualSiblingIfaceImplPlan(t, args, recv.def, method, key)
}

// stdModuleIfacePlan plans the call of std module mod's own impl of its
// interface iface for a std receiver, from that module's functions only.
func (bl *irScalarBuilder) stdModuleIfacePlan(t *ast.Call, args irQualArgs, mod, iface, method string) *irQualPlan {
	if bl.g.std == nil {
		return nil
	}
	var fs []*stdFunc
	for _, f := range bl.g.std.byIface[iface+"."+method][args.kinds[0]] {
		if f.module == mod {
			fs = append(fs, f)
		}
	}
	f := stdPick(fs, args.kinds)
	p := bl.stdFuncPlan(t, args, f)
	if p == nil {
		p = bl.shadowedIfacePlan(t, args, f)
	}
	return p
}

// stdModuleOwnerCall lowers `mod.Owner.method(args)`, the method std module
// mod's type Owner declares, from that module's declarations only, so a
// declaration of the same name elsewhere cannot capture it. handled is false
// only for an operation of a family the VM owns when bareReaches says the
// bare owner names the same std type at the call, which the caller lowers as
// the two-segment `Owner.method(args)`.
func (bl *irScalarBuilder) stdModuleOwnerCall(t *ast.Call, mod, owner, method string, bareReaches bool) (ir.Temp, kind, bool, bool, bool) {
	if !irOwnOperationFamily(owner, method) {
		// `random.Generator.bool()`: a generic std member, or a
		// monomorphic one whose body calls a generic sibling, is
		// instantiated per program as the two-segment
		// `Generator.bool()` is (stdGenericQualCall), from the
		// named module's declaration only.
		if v, rk, mobile, ok, handled := bl.stdInstCallAt(t, bl.g.stdModuleTypeTemplate(mod, owner, method), kindInvalid, nil); handled {
			return v, rk, mobile, ok, true
		}
	} else if bareReaches {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	args := bl.irQualLowerArgs(t)
	plan := bl.stdModuleTypePlan(t, args, mod, owner, method)
	if plan == nil {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, rk, mobile, ok := bl.qualEmit(t, args, plan)
	return v, rk, mobile, ok, true
}

// stdModuleTypePlan resolves `mod.Type.method(args)` for a stdlib module
// `mod`: the overloads of `Type.method` that module declares, picked by the
// operand kinds (stdPick).
func (bl *irScalarBuilder) stdModuleTypePlan(t *ast.Call, args irQualArgs, module, owner, method string) *irQualPlan {
	if bl.g.std == nil || !args.ok {
		return nil
	}
	var fs []*stdFunc
	for _, f := range bl.g.std.byType[owner+"."+method] {
		if f.module == module {
			fs = append(fs, f)
		}
	}
	if len(fs) == 0 {
		return nil
	}
	return bl.stdFuncPlan(t, args, stdPick(fs, args.kinds))
}

// resolvedOwnerPlan calls the function the checker resolved fa to (its
// reference at the field), which another file declares in a block of the
// call's qualifier: an owner function, or an impl block's function at the
// def of the qualifier's declaration or at its instance the checker
// instantiated the call at. Each is found by its declaration rather than
// by a name this file need not see.
func (bl *irScalarBuilder) resolvedOwnerPlan(t *ast.Call, args irQualArgs, fa *ast.FieldAccess) *irQualPlan {
	g := bl.g
	if g.files == nil || fa.Field == nil {
		return nil
	}
	sym := g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return nil
	}
	fd, ok := sym.Node.(*ast.FuncDef)
	if !ok {
		return nil
	}
	for key, sites := range g.files.ownerMembers {
		if key.method != fa.Field.Name {
			continue
		}
		for _, s := range sites {
			if (s.item != nil && s.item.decl == fd) || (s.block != nil && slices.Contains(s.block.Items, ast.Node(fd))) {
				return bl.siblingOwnerPlanFor(t, args, key.recv, sym.OwningType, fa.Field.Name)
			}
		}
	}
	// A function of an impl block of the qualifier's declaration (a
	// validated type's `try_from`, which is its TryFrom impl's): that
	// block's function for this file's def of the type, or of the
	// instance of it the checker instantiated the call at.
	q := g.routeCallee(t).Qualifier
	if q.Kind != analysis.QualType || q.Decl == nil || g.reg == nil {
		return nil
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType {
		return nil
	}
	if len(typeDeclTypeParams(q.Decl)) == 0 {
		if d := g.importedTypeDef(g.checkedRef(ti.Line, ti.Col)); d != nil && d.decl == q.Decl {
			return bl.qualSiblingImplPlanFor(t, args, d, fa.Field.Name)
		}
		return nil
	}
	ref, found := g.foreignTemplateOf(g.reg.byDecl[q.Decl])
	if !found {
		return nil
	}
	for _, it := range ref.tpl.impls {
		if !slices.Contains(it.decl.Items, ast.Node(fd)) {
			continue
		}
		targs, ok := g.implRecvTypeArgs(g, t.Func, it.decl, len(ref.tpl.params))
		if !ok {
			return nil
		}
		k, ok := g.instantiateForeign(ref, targs)
		if !ok || k.tag != tagNamed || k.def == nil {
			return nil
		}
		return bl.qualSiblingImplPlanFor(t, args, k.def, fa.Field.Name)
	}
	return nil
}

// stdOwnerUnshadowed reports whether the bare name owner, written here, still
// names the std type a module qualifier named (`lists.List`): no type,
// generic template or interface this file declares or imports takes it.
func (g *gen) stdOwnerUnshadowed(owner string) bool {
	if g.userTypeNamed(owner) || g.genericTemplates[owner] != nil {
		return false
	}
	_, iface := g.ifaces[owner]
	return !iface
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
