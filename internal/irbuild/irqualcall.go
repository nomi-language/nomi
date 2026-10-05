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
// value before the next argument can emit statements. Checked kinds then
// validate the selected signature without coercion or speculative lowering.
//
// A call names its declaring symbol, including across sibling and cached stdlib
// modules; approved rt bindings carry explicit host crossings. Go spelling and
// required imports travel beside the instruction; import sets change only when
// the consumer emits it, so a discarded build cannot leak an unused import.
//
// Calls inside recorded assertion subjects decline until qualified argument
// rows can preserve the complete operand history in reports.

import (
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// irQualPlan is one resolved qualified callee: an identity, a result kind, and
// the Go realization that travels beside the node.
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
	// args, when set, replaces the call's lowered operands: a callee with
	// parameter defaults the call omits takes its written operands and then
	// each omitted default, evaluated at the call (implDefaultArgs).
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
		} else {
			src, k, mobile, ok = bl.lower(arg)
		}
		if !ok {
			a.kinds[i] = kindInvalid
			a.ok = false
			continue
		}
		// Materialize before the next argument can emit statements. Delaying
		// this copy until qualEmit would reorder effects.
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
	bl.qualTypeEmptyArgs(t, &a)
	if bl.qualRecord == t {
		bl.qualRecordArgs, bl.qualRecordSeen = a, true
	}
	return a
}

// qualTypeEmptyArgs gives an empty collection literal operand (`[]`, `{}`)
// the element types the checker solved for its parameter, so
// `String.join([], "-")` selects `join(List<String>, String)` like a
// non-empty list does. An operand whose checked parameter is not a concrete
// collection kind keeps its empty kind.
func (bl *irScalarBuilder) qualTypeEmptyArgs(t *ast.Call, a *irQualArgs) {
	var ft *analysis.FuncType
	for i, k := range a.kinds {
		if k != kindEmptyList && k != kindEmptyMap && k != kindEmptySet && k != kindEmptyVector {
			continue
		}
		if ft == nil {
			if ft = bl.g.checkedCallSignature(t); ft == nil {
				ft = bl.g.declaredCallSignature(t)
			}
			if ft == nil {
				return
			}
		}
		if i >= len(ft.Params) {
			continue
		}
		want := bl.g.project(ft.Params[i])
		// kindInvalid: lookup — a parameter the checker left unsolved types nothing; the operand keeps its empty kind.
		if want == kindInvalid || want == k {
			continue
		}
		if v, got, ok := bl.coerceEmpty(t.Args[i], a.temps[i], k, want); ok {
			a.temps[i], a.kinds[i] = v, got
		}
	}
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

func (bl *irScalarBuilder) ifaceContainerCall(t *ast.Call, args irQualArgs, iface, method string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	// Inside a std module only an instance body or a test body instantiates,
	// as an owner-qualified `Set.f(...)` call in a test body does.
	if (bl.g.stdModule != "" && !bl.stdInstRangeReceiver(args) && !bl.stdTestOwnType()) || !args.ok || len(args.kinds) == 0 {
		return no()
	}
	if _, local := bl.g.types[iface]; local {
		return no()
	}
	if d, isIface := bl.g.ifaceNamed(iface); isIface && d != nil && bl.g.stdInstCur == nil {
		// A local interface is dispatched through its own table. A std
		// instance's module may declare the interface its container impl
		// implements (`ToJson.to_json(item)` in `List.to_json<List<Note>>`),
		// and that impl is the template checked below.
		return no()
	}
	self := args.kinds[0]
	if iface == "Display" && method == "to_string" && len(args.kinds) == 1 &&
		(irRetainedRecordKind(self) || irRetainedTupleKind(self)) && irScalarLeafParts(self) {
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
	return bl.stdInstCallAt(t, f, self)
}

// implDefaultArgs is a call's operands against an impl item whose trailing
// parameters have defaults the call omits: the written operands, then each
// omitted parameter's default lowered at the call as a direct call's are
// (callDefaults), seeing module functions and the earlier parameters. ok is
// false when an omitted parameter has no default or its default does not
// lower. A call that omits nothing answers its own operands.
func (bl *irScalarBuilder) implDefaultArgs(t *ast.Call, args irQualArgs, it *implItem) (irQualArgs, bool) {
	if !args.ok || len(args.temps) >= len(it.params) || namedArgNode(t.Args) != nil {
		return args, args.ok && len(args.temps) == len(it.params)
	}
	fd := irImplSource(it)
	if fd == nil || len(fd.Params) != len(it.params) {
		return args, false
	}
	for i, k := range args.kinds {
		if k != it.params[i] || !irCallOperandKind(k) {
			return args, false
		}
	}
	temps := make([]ir.Temp, len(it.params))
	copy(temps, args.temps)
	for i := len(args.temps); i < len(temps); i++ {
		temps[i] = ir.NoTemp
	}
	if !bl.callDefaults(&fnSig{decl: fd, params: it.params}, temps) {
		return args, false
	}
	return irQualArgs{temps: temps, kinds: append([]kind(nil), it.params...), mobile: make([]bool, len(temps)), ok: true}, true
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
	want := bl.g.project(unitHoles(ft.Params[i]))
	if want.tag != tagNamed || want.def == nil || want.def.preludeOf == nil || !irRetainedEnumKind(want.def) {
		return kindInvalid, false
	}
	return want, true
}

// declaredCallSignature is the callee's declared function type, for a
// non-generic callee whose reference carries no instantiated CallType.
func (g *gen) declaredCallSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || t.Func == nil {
		return nil
	}
	line, col := calleeRefPos(t.Func)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
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
	if bl.recording > 0 {
		// Inside an assertion subject the call's rows are recorded after the
		// call (recordedQualCall).
		if ti, isType := fa.Object.(*ast.TypeIdent); isType && ti.Name == "Iter" && fa.Field != nil && fa.Field.Name == "loop" && bl.inTest {
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
		return no()
	}
	method := fa.Field.Name
	obj, isIdent := fa.Object.(*ast.Ident)
	ti, isType := fa.Object.(*ast.TypeIdent)
	if isType {
		if canon := bl.g.stdOwnerAlias(ti); canon != "" {
			// `import std/iter.Iter as It`, then `It.count(xs)`: the call
			// is std's `Iter.count`, which every arm below names by its
			// own name.
			ti = &ast.TypeIdent{Name: canon, Line: ti.Line, Col: ti.Col}
		}
	}
	if gt, applied := fa.Object.(*ast.GenericType); applied {
		// `Maybe<Int>.from_json(j)`, as `derive FromJson` writes a field's
		// decode: the instance the type names, as `T.from_json` reaches it
		// when T is bound to that type.
		// kindInvalid: lookup — a type the builder cannot name falls to no(), which declines the call.
		if k := bl.g.typeOf(gt); k != kindInvalid {
			if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, method); handled {
				return v, rk, mobile, ok
			}
		}
		return no()
	}
	if !isIdent && !isType {
		// Shape B: a 3-segment qualifier. A namespaced type name
		// (`Json.DecodeError.to_string`, `shapes.Colour.of`) resolves as
		// that type's owner; anything else declines.
		owner, mod, ok := bl.qualDottedOwner(fa.Object)
		if !ok {
			return no()
		}
		if mod != "" {
			if !irOwnOperationFamily(owner, method) {
				// `random.Generator.bool()`: a generic std member, or a
				// monomorphic one whose body calls a generic sibling, is
				// instantiated per program as the two-segment
				// `Generator.bool()` is (stdGenericQualCall), from the
				// named module's declaration only.
				if v, rk, mobile, ok, handled := bl.stdInstCallAt(t, bl.g.stdModuleTypeTemplate(mod, owner, method), kindInvalid); handled {
					return v, rk, mobile, ok
				}
			}
			args := bl.irQualLowerArgs(t)
			plan := bl.stdModuleTypePlan(t, args, mod, owner, method)
			if plan == nil {
				return no()
			}
			return bl.qualEmit(t, args, plan)
		}
		ti, isType = &ast.TypeIdent{Name: owner, Line: fa.Line, Col: fa.Col}, true
	}
	if isIdent && irQualIsLocal(bl, obj.Name) {
		// Shape C: a bound local's function-valued field, read and then
		// called through the value it holds (`gen.run(seed)`).
		return bl.indirectCall(t)
	}
	if isType && ti.Name == "Iter" && len(t.Args) == 2 {
		switch method {
		case "reduce", "map", "filter", "take_while", "each", "iterate":
			// The operations the VM drives a signalling callback through.
			if ident, named := t.Args[1].(*ast.Ident); named {
				defer bl.markCtlRef(ident, method)()
			}
		}
	}
	if isType && ti.Name == "Iter" && method == "reduce" {
		return bl.iterReduceCall(t)
	}
	if isType && ti.Name == "Struct" && method == "update" {
		return bl.structUpdateCall(t)
	}
	if isType && ti.Name == "Iter" && (method == "map" || method == "filter" || method == "take_while" || method == "each" || method == "iterate") && len(t.Args) == 2 && callbackCarriesSignal(t.Args[1]) {
		lam, ok := t.Args[1].(*ast.Lambda)
		if !ok {
			return no()
		}
		defer bl.markCtl(lam, irCtlAdapter)()
	}
	if isType && ti.Name == "Iter" && method == "loop" {
		// The callback is inlined rather than lowered as a value.
		return bl.iterLoop(t)
	}
	if isType && ti.Name == "Context" && (method == "with_value" || method == "value") {
		// The name must resolve to std's Context, not a local declaration.
		if d, found := bl.g.namedType(ti.Name); found && irContextKind(named(d)) {
			return bl.contextValueCall(t, method)
		}
	}
	if isType {
		if v, rk, mobile, ok, handled := bl.holeBoundCall(t, ti, method); handled {
			return v, rk, mobile, ok
		}
	}
	bl.qualPreResolve(ti, obj, isType, method)
	if isType && method == "equal?" && len(t.Args) == 2 && (ti.Name == "List" || ti.Name == "Maybe" || ti.Name == "Result" || ti.Name == "Vector" || ti.Name == "Map") {
		if _, shadowed := bl.g.namedType(ti.Name); !shadowed {
			return bl.containerEqualCall(t, ti.Name)
		}
	}
	if isType && ti.Name == "Hashable" && method == "hash" {
		if _, local := bl.g.types["Hashable"]; !local {
			if v, rk, mobile, ok, handled := bl.tupleHashCall(t); handled {
				return v, rk, mobile, ok
			}
		}
	}
	if isType && ti.Name == "Equatable" && method == "equal?" && len(t.Args) == 2 && bl.g.stdModule != "" {
		// `Equatable.equal?(a.max_age, b.max_age)` in a std body (a derived
		// impl's field over a `Maybe<Int>`): a std module holds no generic
		// instance, and the container's equality is the structural
		// comparison containerEqualCall lowers.
		if owner := bl.equatableContainerOwner(t); owner != "" {
			return bl.containerEqualCall(t, owner)
		}
	}
	if isType && len(t.Args) == 1 && irPreludePredicate(ti.Name, method) != "" {
		if _, shadowed := bl.g.namedType(ti.Name); !shadowed || bl.stdTestOwnType() {
			return bl.preludePredicateCall(t, ti.Name, method)
		}
	}
	if isType {
		if k, bound := bl.g.genericSubstKind(ti.Name); bound {
			// `T.from_json(item)` inside an instance: T is the instance's
			// concrete argument, and the call is that type's.
			if v, rk, mobile, ok, handled := bl.stdKindQualCall(t, k, method); handled {
				return v, rk, mobile, ok
			}
			return no()
		}
		if v, rk, mobile, ok, handled := bl.stdGenericQualCall(t, ti.Name, method); handled {
			return v, rk, mobile, ok
		}
	}
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	args := bl.irQualLowerArgs(t)
	var plan *irQualPlan
	switch {
	case isType:
		if v, k, mobile, ok := bl.scalarOperatorCall(t, args, ti.Name, method); ok {
			return v, k, mobile, true
		}
		if v, k, mobile, ok, owned := bl.scalarEqualCall(t, args, ti.Name, method); owned {
			return v, k, mobile, ok
		}
		if method == "to_string" && (ti.Name == "Int" || ti.Name == "Float" || ti.Name == "Bool" || ti.Name == "String") {
			return bl.scalarTextCall(t, args, ti.Name)
		}
		if method == "to_string" && ti.Name == "Display" && args.ok && len(args.kinds) == 1 && args.mobile[0] && irDisplayScalar(args.kinds[0]) {
			if _, local := bl.g.types["Display"]; !local && bl.g.stdModule == "" {
				v, k, mobile, ok := bl.scalarTextCall(t, args, args.kinds[0].nomi())
				return v, k, mobile, ok
			}
		}
		if method == "to_string" && ti.Name == "Display" && args.ok && len(args.kinds) == 1 && irStdDisplayExistential(args.kinds[0]) {
			// `Display.to_string(v)` over std Display's existential: the VM
			// renders the value it holds through that value's own impl.
			r := ir.NewRenderDisplayErased(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString})
			return r.Dst(), kindString, false, true
		}
		if ti.Name == "Iter" {
			return bl.iterCall(t, args, method)
		}
		if _, local := bl.g.types[ti.Name]; !local && method == "each_while" && args.ok && len(args.kinds) == 2 &&
			((ti.Name == "Bytes" && args.kinds[0] == irBytesKind()) || (ti.Name == "String" && args.kinds[0] == kindString)) {
			// `Bytes.each_while(b, f)` / `String.each_while(s, f)`: the impl's
			// host fn is the protocol the VM's byte and grapheme views already
			// drive, so the type-qualified call is `Iter.each_while` over that
			// view rather than a crossing no host table answers.
			return bl.iterCall(t, args, "each_while")
		}
		if _, local := bl.g.types[ti.Name]; (!local || bl.g.stdModule != "") && ti.Name == "String" && method == "join" && args.ok && (len(args.kinds) == 1 || len(args.kinds) == 2) {
			// `String.join(parts, separator)` drives any Iter<String> once,
			// so it is an Iter terminal over the source's view. Inside std
			// the String type is its own module's, which reads as local. The
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
			return bl.iterCall(t, args, "join")
		}
		if ti.Name == "Range" {
			return bl.rangeCall(t, args, method)
		}
		if v, k, mobile, ok := bl.synthDebugRender(t, args, ti.Name, method); ok {
			return v, k, mobile, true
		}
		if v, k, mobile, ok, handled := bl.ifaceContainerCall(t, args, ti.Name, method); handled {
			return v, k, mobile, ok
		}
		if method == "to_string" && ti.Name == "Display" && args.ok && len(args.kinds) == 1 && irStructuralDisplayKind(args.kinds[0]) {
			// `Display.to_string(x)` over a value with no nominal part, as
			// std's own instances call it on an element (`List.to_string<Bool>`
			// renders each Bool this way): the text is structural, which
			// rt.DisplayText is.
			if _, local := bl.g.types["Display"]; !local {
				r := ir.NewRenderDisplay(bl.g.irNodePos(t.Args[0]), bl.f.NewTemp(), args.temps[0])
				bl.b.Append(r)
				bl.side(r.Dst(), irScalarSide{k: kindString})
				return r.Dst(), kindString, false, true
			}
		}
		if ti.Name == "Result" && method == "map_err" {
			plan = bl.resultMapErrPlan(t, args)
		} else if ti.Name == "Maybe" && method == "to_result" {
			plan = bl.preludeToResultPlan(t, args)
		} else if (ti.Name == "Maybe" || ti.Name == "Result") && method == "with_default" {
			plan = bl.preludeWithDefaultPlan(t, args, ti.Name)
		} else if (ti.Name == "List" || ti.Name == "Vector") && method == "compare" {
			return bl.containerCompareAny(t, args, ti.Name)
		} else if ti.Name == "Map" {
			plan = bl.mapCallPlan(t, args, method)
		} else if ti.Name == "List" {
			plan = bl.listCallPlan(t, args, method)
		} else if ti.Name == "Vector" {
			plan = bl.vectorCallPlan(t, args, method)
		} else if ti.Name == "Set" {
			plan = bl.setCallPlan(t, args, method)
		} else if p := bl.synthStdFilePlan(t, args, ti, method); p != nil {
			plan = p
		} else {
			plan = bl.qualImplPlan(t, args, ti.Name, method)
		}
		if plan == nil {
			plan = bl.siblingReceiverPlan(t, args, ti.Name, method)
		}
		if plan == nil && (ti.Name == "Map" || ti.Name == "Set" || ti.Name == "Vector" || ti.Name == "List") && (bl.g.stdModule == "" || bl.stdTestOwnType()) {
			// A Map, Set or Vector function with no intrinsic is std's Nomi body,
			// instantiated at the call's types like any generic std member.
			// kindInvalid: sentinel — no receiver kind selects the member.
			self := kindInvalid
			if args.ok && len(args.kinds) > 0 && irContainerBaseName(args.kinds[0]) == ti.Name {
				// An impl member (`impl Display for Vector<T>`) is solved
				// from its receiver, which the first operand is.
				self = args.kinds[0]
			}
			prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
			bl.stdInstPreCall, bl.stdInstPreArgs = t, args
			v, rk, mobile, ok, handled := bl.stdInstCallAt(t, bl.g.stdGenericTemplate(ti.Name, method), self)
			bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs
			if handled {
				return v, rk, mobile, ok
			}
		}
	case irQualIsLocal(bl, obj.Name):
		// Shape C: a bound local's member holding a callable, which is
		// `CalleeIndirect` and a different resolution.
		return no()
	default:
		plan = bl.qualFilePlan(t, args, obj, method)
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
	if g.files == nil || irQualIsLocal(bl, obj.Name) {
		return
	}
	to, isSibling := g.files.lookupQualifier(g.fa, obj)
	if !isSibling {
		return
	}
	f := g.files.units[to].funcs[method]
	if f == nil || !f.lowerable() {
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

// irQualIsLocal reports whether the qualifier is a name this scope BOUND,
// which `qualifiedCall` reads the same way: "the owner is a LOCAL, so this is
// a field read followed by a call on the value it holds, not a qualifier at
// all".
func irQualIsLocal(bl *irScalarBuilder, name string) bool {
	for scope := bl; scope != nil; scope = scope.parent {
		if _, bound := scope.boundK[name]; bound {
			return true
		}
		if scope.sh != nil {
			if _, param := scope.sh.params[name]; param {
				return true
			}
		}
	}
	_, bound := bl.g.lookup(name)
	return bound
}

// qualImplPlan resolves `Owner.method(args)`: this module's own impl blocks
// first, the way `typeQualifiedCall` resolves them, then the stdlib index the
// way `stdlibCall` does.
//
// A local receiver's implementation takes precedence over a stdlib type with
// the same spelling. The stdlib resolver independently rejects local shadows.
func (bl *irScalarBuilder) qualImplPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	if p := bl.operQualPlan(args, owner, method); p != nil {
		return p
	}
	key := owner + "." + method
	if d, isIface := bl.g.ifaceNamed(owner); isIface && d != nil {
		return bl.concreteInterfacePlan(t, args, d, method)
	}
	if p := bl.foreignIfacePlan(t, args, owner, method); p != nil {
		return p
	}
	k := bl.g.qualifierKind(owner)
	if tpl := bl.g.genericTemplates[owner]; tpl != nil {
		// `Wrapped.describe(boxed)`: a generic template names the instance
		// its receiver operand is.
		if r := args.kinds; args.ok && len(r) > 0 && r[0].tag == tagNamed && r[0].def != nil && r[0].def.genericOf == tpl {
			k = r[0]
		} else if inst, ok := bl.g.templateCallInstance(t, tpl, method); ok {
			// `Span.new(1, 5)`: no operand is an instance, so the instance
			// is the one the checker's solved signature names.
			k = inst
		}
	}
	// kindInvalid: lookup — asks whether `owner` names a type at all, to choose between the local-impl path and falling through; a miss returns nil and the caller reports the qualified callee it could not resolve.
	if k != kindInvalid {
		var only *implItem
		for _, d := range bl.g.implsOf(k) {
			if d.recv != k || !d.lowerable {
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
				// `typeQualifiedCall` reports `ambiguous type-qualified
				// call` for this, which makes the programmer
				// disambiguate. Declined, so that report survives.
				return nil
			}
			only = item
		}
		if only != nil {
			var filled *irQualArgs
			if args.ok && len(args.temps) < len(only.params) && only.lowerable {
				// `Box.announce(b)` omitting a defaulted parameter.
				full, ok := bl.implDefaultArgs(t, args, only)
				if !ok {
					return nil
				}
				filled = &full
				args = full
				t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
			}
			if !bl.qualSignature(t, args, only.params, only.result) || !only.lowerable {
				return nil
			}
			return &irQualPlan{token: only, name: key, result: only.result, args: filled}
		}
	}
	if tpl := bl.g.genericTemplates[owner]; tpl != nil {
		if p := bl.hostTemplateImplPlan(t, args, tpl, method); p != nil {
			return p
		}
	}
	if p := bl.qualSiblingImplPlan(t, args, owner, method); p != nil {
		return p
	}
	if args.ok && len(args.kinds) > 0 {
		if p := bl.callerImplPlan(t, args, owner, method, args.kinds[0]); p != nil {
			return p
		}
	}
	if p := bl.stdIfacePlan(t, args, owner, method); p != nil {
		return p
	}
	if p := bl.stdMethodCallPlan(t, args, owner, method); p != nil {
		return p
	}
	return bl.siblingReceiverPlan(t, args, owner, method)
}

// siblingReceiverPlan resolves `Display.to_string(shapes.origin())`: an
// interface call whose receiver another file declares reaches that file's
// impl, written or synthesized, as `shapes.Point.to_string` would. The named
// interface's member first, so a method name two of the receiver's impls
// share (`Comparable.compare`, `Ranked.compare`) is the one the call names.
//
// Every route that plans an interface call goes through here, so `io.print(p)`
// and `"${p}"` reach the impl the written `Display.to_string(p)` reaches.
func (bl *irScalarBuilder) siblingReceiverPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	// kindInvalid: lookup — the owner names no type, so it is an interface whose receiver another file may declare.
	if !args.ok || len(args.kinds) == 0 || args.kinds[0].tag != tagNamed || args.kinds[0].def == nil ||
		bl.g.qualifierKind(owner) != kindInvalid {
		return nil
	}
	if p := bl.qualSiblingIfaceImplPlan(t, args, args.kinds[0].def, method, owner); p != nil {
		return p
	}
	return bl.qualSiblingImplPlanFor(t, args, args.kinds[0].def, method)
}

// operQualPlan resolves the qualified spellings of an operator impl on a
// declared receiver: `Add.add(Day(10), Days(4))`, interface-qualified, and
// `Day.add(Day(10), Weeks(2))`, type-qualified, whose receiver may implement
// one operator interface at several right-hand types. The impl is the one
// `Day(10) + Days(4)` selects (operImplFor), so the operator and its two
// qualified spellings reach one declaration.
func (bl *irScalarBuilder) operQualPlan(args irQualArgs, owner, method string) *irQualPlan {
	if !args.ok || len(args.kinds) != 2 {
		return nil
	}
	lk, rk := args.kinds[0], args.kinds[1]
	if lk.def == nil || isDecimalKind(lk) {
		return nil
	}
	var spec *operIfaceSpec
	if s := operIfaceByName[owner]; s != nil && s.method == method {
		if _, local := bl.g.types[owner]; local {
			return nil
		}
		spec = s
	} else if bl.g.qualifierKind(owner) == lk {
		for i := range operIfaceSpecs {
			if operIfaceSpecs[i].method == method {
				spec = &operIfaceSpecs[i]
			}
		}
	}
	if spec == nil {
		return nil
	}
	it, _, rival := bl.g.operImplFor(spec.nomi, lk, spec.method, []kind{lk, rk})
	if rival || it == nil || !it.lowerable || len(it.params) != 2 || len(it.params0) != 2 || it.params[0] != lk || it.params[1] != rk || !irCallableValueKind(it.result) {
		return nil
	}
	return &irQualPlan{token: it, name: spec.nomi + "." + spec.method, result: it.result}
}

// qualSiblingImplPlan resolves `Owner.method(args)` to an impl function a
// SIBLING FILE declares for Owner, tried after the local impl route: the one
// written site outside this unit, no import cycle, and the declaring package's
// kinds translated into this one before the signature is compared. Anything
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
// one interface when iface is not empty: `Loud.say(q)` where a sibling file
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
	if d.instOrigin != nil {
		// Another file's generic instance: its impls are the owner's
		// instance's, not the template's written sites.
		return bl.siblingInstanceImplPlan(t, args, d, method, iface)
	}
	chosen, params, result, owning := g.siblingImplMember(d, method, iface)
	if chosen == nil {
		return nil
	}
	f := chosen.fn
	if f.defaults && namedArgNode(t.Args) != nil {
		return nil
	}
	to := chosen.unit
	var filled *irQualArgs
	if f.defaults && args.ok && len(args.temps) < len(params) {
		// `Powered.describe(e)` omitting a parameter the declaring file
		// defaults: the default is built here, when every name it mentions
		// resolves to the same declaration from both files.
		full, ok := bl.siblingDefaultArgs(t, args, f, params, owning)
		if !ok {
			return nil
		}
		filled = &full
		args = full
		t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
	}
	if !bl.qualSignature(t, args, params, result) {
		return nil
	}
	unit := g.files.units[to]
	p := &irQualPlan{token: f, name: unit.key + "." + d.nomi + "." + method, result: result, args: filled}
	p.sym = owning.irCalleeSym(chosen.item, chosen.symName)
	return p
}

// siblingImplMember is the one impl function another file declares for its
// type d under `method`, narrowed to iface's impls when iface is not empty,
// with its parameter and result kinds translated into this unit and the gen
// that declares it. A nil site is no such function, more than one (native
// reports `ambiguous type-qualified call`), or one this unit cannot type.
func (g *gen) siblingImplMember(d *typeDef, method, iface string) (*implMemberSite, []kind, kind, *gen) {
	if g.files == nil || g.fileUnit < 0 || g.reg == nil || d == nil || d.decl == nil {
		return nil, nil, kindInvalid, nil
	}
	var chosen *implMemberSite
	for _, site := range writtenImplMembers(g.files.implMembers[implMemberKey{recv: d.decl, method: method}]) {
		if site.unit == g.fileUnit || (iface != "" && site.iface != iface) {
			continue
		}
		if chosen != nil {
			return nil, nil, kindInvalid, nil
		}
		site := site
		chosen = &site
	}
	if chosen == nil || chosen.item == nil || !chosen.fn.lowerable() {
		return nil, nil, kindInvalid, nil
	}
	f := chosen.fn
	params := make([]kind, len(f.params))
	for i, p := range f.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil, nil, kindInvalid, nil
		}
		params[i] = ip
	}
	result, ok := g.importKind(f.result)
	if !ok {
		return nil, nil, kindInvalid, nil
	}
	owning := g.reg.gens[chosen.unit]
	if owning == nil {
		return nil, nil, kindInvalid, nil
	}
	return chosen, params, result, owning
}

// siblingDefaultArgs fills the parameters a short call to a sibling file's
// impl function omits from their declared defaults, lowered here. A default
// whose names resolve differently in the two files declines, for
// portableDefault's reason.
func (bl *irScalarBuilder) siblingDefaultArgs(t *ast.Call, args irQualArgs, f *fileFunc, params []kind, owning *gen) (irQualArgs, bool) {
	g := bl.g
	if len(f.params0) != len(params) || owning.fa == nil || g.fa == nil {
		return args, false
	}
	for i, k := range args.kinds {
		if k != params[i] || !irCallOperandKind(k) {
			return args, false
		}
	}
	for i := len(args.temps); i < len(params); i++ {
		d := f.params0[i].Default
		if d == nil {
			return args, false
		}
		names := map[string]bool{}
		walkNames(d, names, pickAnyName)
		for name := range names {
			there, here := resolvedTypeSymbol(owning.fa, name), resolvedTypeSymbol(g.fa, name)
			if (there == nil) != (here == nil) || (there != nil && there.Node != here.Node) {
				return args, false
			}
		}
	}
	temps := make([]ir.Temp, len(params))
	copy(temps, args.temps)
	for i := len(args.temps); i < len(temps); i++ {
		temps[i] = ir.NoTemp
	}
	if !bl.callDefaults(&fnSig{decl: &ast.FuncDef{Params: f.params0}, params: params}, temps) {
		return args, false
	}
	return irQualArgs{temps: temps, kinds: append([]kind(nil), params...), mobile: make([]bool, len(temps)), ok: true}, true
}

// qualFilePlan resolves `owner.method(args)` where owner is a FILE: a sibling
// file of this Nomi module, or a stdlib file's API object.
//
// SIBLING BEFORE STDLIB, which is `qualifiedCall`'s own order and its own
// reason — "`io` is a stdlib file and never resolves here".
func (bl *irScalarBuilder) qualFilePlan(t *ast.Call, args irQualArgs, ownerID *ast.Ident, method string) *irQualPlan {
	owner := ownerID.Name
	if bl.g.files != nil {
		if to, isSibling := bl.g.files.lookupQualifier(bl.g.fa, ownerID); isSibling {
			return bl.qualSiblingPlan(t, args, to, method)
		}
	}
	// Stdlib file APIs have separate generic, testing and host-call routes.
	// They are outside the concrete type-qualified method path, except the
	// hosts with VM adapters (RtFuncs rows, with or without the frame).
	std, isStd := stdFileQualifier(bl.g.fa, ownerID)
	if !isStd {
		std, isStd = bl.stdOwnFileQualifier(owner)
	}
	if isStd && bl.g.std != nil {
		if f := bl.g.std.byFile[std+"."+method]; f != nil && f.why == "" && irFrameHost(f) && bl.qualSignature(t, args, f.params, f.result) {
			return &irQualPlan{token: f, name: f.key, result: f.result, host: true}
		}
		if f := bl.g.std.byFile[std+"."+method]; f != nil && f.why == "" && irScalarHost(f) && bl.qualSignature(t, args, f.params, f.result) {
			return &irQualPlan{token: f, name: f.key, result: f.result, host: true}
		}
		if f := bl.g.std.byFile[std+"."+method]; f != nil && f.why == "" {
			if _, compiler := irCompilerHost(f); compiler {
				return bl.stdFuncPlan(t, args, f)
			}
		}
	}
	return nil
}

// irFrameHost admits an RtFuncs row that takes the caller's frame: the VM's
// generated adapter passes the call's runtime frame, so a cancellation reaches
// the rt function as it does in Go.
func irFrameHost(f *stdFunc) bool {
	_, row := stdlibHostFuncs[f.key]
	return row && f.rtFrame
}

// synthStdFilePlan plans `json.shape_error_prepend(...)` written by derive
// synthesis with a type-spelled file qualifier, resolved against std's
// free-function table.
func (bl *irScalarBuilder) synthStdFilePlan(t *ast.Call, args irQualArgs, owner *ast.TypeIdent, method string) *irQualPlan {
	if bl.g.std == nil || !analysis.IsSynthesizedLine(owner.Line) || owner.Name == "" || !unicode.IsLower([]rune(owner.Name)[0]) {
		return nil
	}
	return bl.stdFuncPlan(t, args, bl.g.std.byFile[owner.Name+"."+method])
}

// qualSiblingPlan resolves a sibling file's top-level `fn` called with every
// argument written positionally. A call that names an argument or omits a
// defaulted one is siblingCall's, which bl.call routes it to first.
//
// THE CYCLE REFUSAL IS ASKED, and it has to be: `siblingCycle` is FAIL-SAFE
// rather than live — its own header says the component partition means a real
// cycle takes a different arm — so a producer that skipped it would emit a
// call across an edge that closes a Go import cycle.
func (bl *irScalarBuilder) qualSiblingPlan(t *ast.Call, args irQualArgs, to int, method string) *irQualPlan {
	unit := bl.g.files.units[to]
	f := unit.funcs[method]
	if f != nil && f.generic {
		params, result, sym, ok := bl.g.siblingGenericInstance(t, to, f)
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
// from the declaring unit into this one. The translation is also what
// declares this package's alias for a sibling type the signature names.
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
// and lowered as bl.call places a local callee's (argSlotPlan), and each
// omitted parameter is filled by the declaring unit's accessor for it
// (siblingFill); the call links to the declaring unit's symbol as
// qualSiblingPlan's does.
func (bl *irScalarBuilder) siblingCall(t *ast.Call, site fileSite) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	f := site.fn
	if f == nil || f.decl == nil {
		return no()
	}
	var params []kind
	var result kind
	var sym *ir.Symbol
	ok := false
	if f.generic && !site.host {
		params, result, sym, ok = bl.g.siblingGenericInstance(t, site.unit, f)
	} else if f.lowerable() {
		params, result, ok = bl.g.siblingSignature(f)
	}
	if !ok || len(params) != len(f.decl.Params) || (!irCallableValueKind(result) && result != kindUnit) {
		return no()
	}
	if site.host && !irHostFnHasVMBody(params, result) {
		// irHostFnRetain builds no VM body for a Go-bound declaration that
		// takes or returns a function, so the call would name a declaration
		// no module holds.
		irDeclineNote("a `go`-bound sibling declaration with no VM body")
		return no()
	}
	names := make([]string, len(f.decl.Params))
	for i, p := range f.decl.Params {
		names[i] = p.Name
	}
	slots, planned := argSlotPlan(t.Args, params, names)
	if !planned {
		return no()
	}
	temps := make([]ir.Temp, len(params))
	mobile := make([]bool, len(params))
	for i := range temps {
		temps[i] = ir.NoTemp
	}
	for _, i := range slots.order {
		a, slot := argExpr(t.Args[i]), slots.slots[i]
		src, k, held, ok := bl.lowerTypedOperand(a, params[slot])
		if !ok || !irCallOperandKind(k) {
			return no()
		}
		// Only the final operand may stay unforced; inside an assertion
		// subject it is named twice, in its row and in the call.
		if src, k, ok = bl.coerceEmpty(a, src, k, params[slot]); !ok || k != params[slot] {
			return no()
		}
		temps[slot], mobile[slot] = src, held
	}
	plan := bl.g.siblingFuncPlan(site.unit, f, result)
	if sym != nil {
		// A generic declaration's instance, interned in the declaring unit.
		plan.sym = sym
	}
	if len(t.Args) != len(params) && (site.host || !bl.siblingFill(t, bl.g.unitGen(site.unit), plan.sym, f.decl, params, temps, mobile)) {
		// A Go-bound declaration's defaults have no accessor.
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
// NIL WHEN THERE IS NO PROGRAM TO LINK, which is `lowerModuleWithStdlib`'s
// path: no registry, so no sibling gens, so the caller keeps interning in its
// own table exactly as it did. `qualSiblingPlan` is unreachable there anyway —
// `g.files` is nil — and the guard is the fence rather than the mechanism.
//
// SEQUENTIAL BY CONSTRUCTION. `ir.Table` needs no lock because it is one
// lowering's, and `Generate` emits units in order on one goroutine; this adds
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
		if k != params[i] && (!bl.g.irErases(params[i], k)) {
			return false
		}
	}
	return true
}

// qualEmit appends the call and records what the Go consumer needs.
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

// irDisplayScalar is a kind whose `Display.to_string` is the scalar renderer
// used for `Int.to_string` and its siblings. The Render records a fresh line
// directive, and its operand must emit no statement so the reset lands at the
// call.
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
	if !args.ok || len(args.kinds) != 1 || !irScalarLeafKind(args.kinds[0]) || args.kinds[0] != bl.g.qualifierKind(owner) {
		return ir.NoTemp, kindInvalid, false, false
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
	if k.tag == tagFunc && bl.g.implsByIface["Debug"][k] == nil {
		// `Debug.inspect` of any function value is a name-free constant
		// (rt/funcname.go's row six). The operand was evaluated.
		c := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), rt.FunctionInspectText())
		bl.b.Append(c)
		return c.Dst(), kindString, false, true
	}
	if k.tag == tagNamed && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def) || irRetainedMarker(k.def)) && bl.g.implsByIface["Debug"][k] != nil {
		plan, structural := bl.distinctDebugPlan(k)
		if plan != nil {
			return bl.qualEmit(t, args, plan)
		}
		if !structural {
			return no()
		}
	} else if irExistentialKind(k) {
		// A local interface's existential holds its concrete value in the
		// VM: the renderer is told every implementer's Debug body.
		impls, ok := bl.existentialDebugImpls(k)
		if !ok {
			return no()
		}
		r := ir.NewRenderDebugWith(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0], impls)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), kindString, false, true
	} else if bl.g.implsByIface["Debug"][k] != nil {
		return no()
	} else if !irStructuralDebugKind(k) {
		// A container of nominal values, such as a std struct's list of
		// rows: the renderer names each nested type's Debug body. Resolving
		// those bodies marks their Go packages; the Go reader spells the
		// render with the native renderer, which marks what it names, so the
		// resolution's marks are dropped.
		impls, ok := bl.nestedDebugImpls(k)
		if !ok {
			return no()
		}
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
	r := ir.NewRenderDebug(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps[0])
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst(), kindString, false, true
}

// qualDottedOwner resolves a 3-segment qualifier's owner. A namespaced type
// name (`Json.DecodeError`, `shapes.Colour`) answers its whole spelling, which
// the type-qualified route resolves like any other owner. A stdlib file
// qualifier followed by a type (`random.Seed`) answers the bare type name and
// the module, because stdlib types are indexed by bare name and a local type
// of the same name must not capture the call.
func (bl *irScalarBuilder) qualDottedOwner(n ast.Node) (owner, stdModule string, ok bool) {
	inner, isFA := n.(*ast.FieldAccess)
	if !isFA || inner.Field == nil {
		return "", "", false
	}
	if name, dotted := bl.g.dottedTypeQualifier(n); dotted {
		return name, "", true
	}
	mod, isIdent := inner.Object.(*ast.Ident)
	if !isIdent || irQualIsLocal(bl, mod.Name) {
		return "", "", false
	}
	std, isStd := stdFileQualifier(bl.g.fa, mod)
	if !isStd {
		return "", "", false
	}
	return inner.Field.Name, std, true
}

// stdModuleTypePlan resolves `mod.Type.method(args)` for a stdlib module
// `mod`: the overloads of `Type.method` that module declares, picked by the
// operand kinds as native stdlibCall picks.
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

// stdOwnerAlias is the std type an owner qualifier names under an import
// alias (`import std/iter.Iter as It` makes `It` name `Iter`), or "" when the
// qualifier is not such an alias. The canonical name must not name a type
// this file declares, which would make it mean something else here.
func (g *gen) stdOwnerAlias(ti *ast.TypeIdent) string {
	if g.fa == nil || ti == nil {
		return ""
	}
	sym := g.fa.References[analysis.Pos{Line: ti.Line, Col: ti.Col}]
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Name == "" || sym.Name == ti.Name {
		return ""
	}
	if _, local := g.types[sym.Name]; local {
		return ""
	}
	if _, local := g.ifaces[sym.Name]; local {
		return ""
	}
	for _, scope := range g.fa.StdlibModuleScopes {
		if scope == nil {
			continue
		}
		if real := scope.LookupLocal(sym.Name); real != nil && (real == sym || real.Resolved == sym) {
			return sym.Name
		}
	}
	return ""
}
