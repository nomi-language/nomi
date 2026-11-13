package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A qualified call with named arguments, `Time.new(minute: m, hour: h)`.
//
// The operands are evaluated in Nomi's order — positionals first,
// then named arguments, each partition in written order (argSlotPlan) — and
// then the call is planned as the same call
// written positionally in parameter order, each operand a synthetic node
// that answers the temporary it was already evaluated into (irScalarBuilder.
// placed). So every qualified-call planner serves the named form unchanged.
//
// The parameter names come from the callee's declaration, which the checker
// resolved the field reference to. A slot the named arguments leave out
// before a supplied one takes its default before the call is planned.

// irPlacedArg is one operand a named-argument call already evaluated.
type irPlacedArg struct {
	temp ir.Temp
	k    kind
}

func (bl *irScalarBuilder) namedQualCall(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if fa.Field == nil || bl.g.fa == nil {
		return no()
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return no()
	}
	if ti, isType := fa.Object.(*ast.TypeIdent); isType && ti.Name == "Struct" && fa.Field.Name == "update" && !bl.g.isBoundTypeParam("Struct") {
		// `Struct.update(updates: {x: 1}, original: p)`: the patch must stay
		// a literal the merge reads, which a stand-in for an evaluated
		// temporary is not, so the update places its own arguments.
		if bl.recording > 0 {
			return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) { return bl.structUpdateCall(t) })
		}
		return bl.structUpdateCall(t)
	}
	// An interface-qualified call (`Display.to_string(value: x)`) names the
	// interface method's parameters; the planned positional call dispatches
	// as any other.
	// A host function (`String.length(s: x)`, `Map.get(m: c, key: k)`)
	// names its parameters as a Nomi-bodied one does, and its defaults are
	// written the same way, so it takes the same path.
	var params []ast.Param
	switch def := sym.Node.(type) {
	case *ast.FuncDef:
		params = def.Params
	case *ast.ExternFunc:
		params = def.Params
	case *ast.InterfaceMethod:
		params = def.Params
	default:
		return no()
	}
	// A destructuring parameter's name is the parser's synthesized one,
	// which no named argument can spell, so only a positional fills it.
	names := make([]string, len(params))
	// argSlotPlan routes a trailing name (`Job.new("k", max_attempts: 3,
	// send)`) to a last parameter of function type, as the checker does,
	// so it needs to know which parameters are functions; nothing else of
	// their kinds is read here.
	slotKinds := make([]kind, len(params))
	for i, p := range params {
		names[i] = p.Name
		if _, fn := p.TypeAnnotation.(*ast.FuncType); fn {
			slotKinds[i] = kind{tag: tagFunc}
		}
	}
	plan, planned := argSlotPlan(t.Args, slotKinds, names)
	if !planned {
		return no()
	}
	if len(t.Args) == 1 && len(names) == 1 && bl.recording == 0 {
		// `io.print(value: x)` is `io.print(x)`: one operand has one order.
		// The output primitives render their operand from its own node, so
		// the call keeps the written expression rather than a stand-in.
		positional := &ast.Call{Func: t.Func, Args: []ast.Node{argExpr(t.Args[0])}, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
		if dst, k, mobile, ok := bl.hostOutput(positional, fa); ok {
			return dst, k, mobile, true
		}
	}
	filled := make([]bool, len(names))
	last := -1
	for _, s := range plan.slots {
		if s < 0 || s >= len(names) {
			return no()
		}
		filled[s] = true
		last = max(last, s)
	}
	temps := make([]ir.Temp, len(names))
	kinds := make([]kind, len(names))
	for i := range temps {
		temps[i] = ir.NoTemp
	}
	// An iteration operation's callback given by name (`Iter.find(xs, f:
	// is_big)`) is widened to its signalling form as typeQualCall widens a
	// positional one, before it is lowered here.
	ti, typeOwner := fa.Object.(*ast.TypeIdent)
	signalOwner := typeOwner && len(names) == 2 && bl.signallingOwner(ti.Name, fa.Field.Name) &&
		(ti.Name != "Iter" || !irSeededIterMethod(fa.Field.Name))
	for _, i := range plan.order {
		a := argExpr(t.Args[i])
		if signalOwner && plan.slots[i] == 1 {
			if ref := irFuncRefNode(a); ref != nil {
				defer bl.markCtlRef(ref)()
			}
			undo, ok := bl.markSignalCallback(a)
			if !ok {
				return no()
			}
			defer undo()
		}
		src, k, _, ok := bl.lower(a)
		if !ok || !irCallOperandKind(k) {
			return no()
		}
		temps[plan.slots[i]], kinds[plan.slots[i]] = src, k
	}
	gap := false
	for i := 0; i < last; i++ {
		gap = gap || !filled[i]
	}
	if gap {
		// `Date.new(2026, day: 5)`: a slot omitted before a supplied one
		// takes its default before the call is planned, after the written
		// operands, as every omitted slot does (fillCallDefaults). The
		// planners then see every slot. An omitted slot's kind is the
		// function's the call reaches, when another file declares it, else
		// the checker's: the call's instantiated signature when it
		// recorded one (`other: Maybe<T> = None`), or the parameter's
		// declared type.
		rec := bl.g.callDefaultsOf(t)
		if rec == nil || len(rec.Params) != len(temps) {
			panic("irbuild: a call leaving out parameters with no CallDefaults record over its slots")
		}
		home, params := bl.namedCallHome(t, fa, rec, kinds, temps)
		if params != nil && len(params) != len(temps) {
			return no()
		}
		checked := bl.g.checkedCallSignature(t)
		if checked != nil && len(checked.Params) != len(temps) {
			checked = nil
		}
		for i := range temps {
			if temps[i] != ir.NoTemp {
				continue
			}
			switch {
			case params != nil:
				kinds[i] = params[i]
			case checked != nil:
				kinds[i] = bl.g.project(checked.Params[i])
			default:
				kinds[i] = bl.g.paramKind(rec.Params[i])
			}
			if !irCallOperandKind(kinds[i]) {
				return no()
			}
		}
		var omitted []int
		for i, tmp := range temps {
			if tmp == ir.NoTemp {
				omitted = append(omitted, i)
			}
		}
		if byAccessor(defaultParams(rec, home, omitted), home, omitted) {
			// The declaring file's accessors take its parameter kinds: a
			// written operand of an interface-typed slot enters it by
			// erasure first, as a positional call's does
			// (qualEraseSlotted).
			for i, tmp := range temps {
				switch {
				case tmp == ir.NoTemp || kinds[i] == params[i]:
				case irExistentialKind(params[i]):
					v, got, erased := bl.coerceEmpty(t, tmp, kinds[i], params[i])
					if !erased {
						return no()
					}
					temps[i], kinds[i] = v, got
				default:
					return no()
				}
			}
		}
		if !bl.fillCallDefaults(t, kinds, temps, nil, home) {
			return no()
		}
		last = len(temps) - 1
	}
	args := make([]ast.Node, last+1)
	placed := make(map[ast.Node]irPlacedArg, len(args))
	for slot := range args {
		line, col := t.Line, t.Col
		for i, s := range plan.slots {
			if s == slot {
				line, col = nodePos(argExpr(t.Args[i]))
			}
		}
		stand := &ast.Ident{Name: "\x00named", Line: line, Col: col}
		args[slot] = stand
		placed[stand] = irPlacedArg{temp: temps[slot], k: kinds[slot]}
	}
	if bl.placed == nil {
		bl.placed = map[ast.Node]irPlacedArg{}
	}
	for n, p := range placed {
		bl.placed[n] = p
	}
	defer func() {
		for n := range placed {
			delete(bl.placed, n)
		}
	}()
	positional := &ast.Call{Func: t.Func, Args: args, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
	if bl.recording == 0 {
		return bl.qualCall(positional, fa)
	}
	// Inside an assertion subject the rows name the written arguments, by
	// the slots they filled, as a direct call's do (recordCallSlots); the
	// stand-ins record nothing.
	//
	// The positional call goes through the whole dispatch, as a call
	// written positionally outside an assertion would: one that leaves a
	// std function's trailing defaults out (`Int.format(n, l, grouping:
	// False)`) is a std short call, which qualCall alone does not reach.
	recording := bl.recording
	bl.recording = 0
	v, k, mobile, ok := bl.call(positional)
	bl.recording = recording
	if ok {
		bl.recordCallSlots(t.Args, plan, names, temps, kinds, k)
	}
	return v, k, mobile, ok
}

// hasNamedArg reports whether any argument is written by name.
func hasNamedArg(args []ast.Node) bool {
	for _, a := range args {
		if _, isNamed := a.(*ast.NamedArg); isNamed {
			return true
		}
	}
	return false
}

// namedCallHome is where the callee of t, a named-argument call
// `Owner.f(...)` leaving a slot out, is declared, for fillCallDefaults, with
// its parameter kinds when the builder reaches it as another file's
// function: a sibling file's inherent impl member, whose file builds
// accessors for its defaults (irImplSlotDefaults), an impl function of
// another file's generic type's instance (instImplAccessorsFor), or, for
// an interface function the call dispatches on a type parameter, the impl
// function this instance reaches, whose own defaults it takes.
func (bl *irScalarBuilder) namedCallHome(t *ast.Call, fa *ast.FieldAccess, rec *analysis.DefaultedCall, kinds []kind, temps []ir.Temp) (defaultHome, []kind) {
	g := bl.g
	switch d := rec.Decl.(type) {
	case *ast.FuncDef:
		if g.reg != nil && g.files != nil {
			if params, home, found := bl.siblingMemberHome(fa, d); found {
				return home, params
			}
			if params, home, found := bl.instanceMemberHome(t, d); found {
				return home, params
			}
		}
	case *ast.InterfaceMethod:
		if it := bl.reachedImplItem(d, kinds, temps); it != nil {
			fd := irImplSource(it)
			return defaultHome{params: fd.Params}, it.params
		}
	}
	return defaultHome{owner: g.fileGen(rec.File)}, nil
}

// reachedImplItem is the impl function of this file's impl block that a
// call of interface function im reaches at this instance's receiver, the
// operand at im's self slot, or nil when the receiver is an interface value
// or the impl is another file's.
func (bl *irScalarBuilder) reachedImplItem(im *ast.InterfaceMethod, kinds []kind, temps []ir.Temp) *implItem {
	recvAt := selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt()
	if recvAt < 0 || recvAt >= len(temps) || temps[recvAt] == ir.NoTemp {
		return nil
	}
	recv := kinds[recvAt]
	if recv.tag == tagIface {
		return nil
	}
	for _, d := range bl.g.ifaces {
		if m := d.methods[im.Name]; m == nil || m.decl != im {
			continue
		}
		impl := bl.g.implsByIface[bl.g.implsKey(d)][recv]
		if impl == nil || !impl.lowerable {
			return nil
		}
		it := impl.items[im.Name]
		if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != len(temps) {
			return nil
		}
		return it
	}
	return nil
}

// siblingMemberHome is the parameter kinds and default home of fd when it
// is a sibling file's inherent impl function `Type.f` names through fa.
func (bl *irScalarBuilder) siblingMemberHome(fa *ast.FieldAccess, fd *ast.FuncDef) ([]kind, defaultHome, bool) {
	g := bl.g
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType || fa.Field == nil {
		return nil, defaultHome{}, false
	}
	d, found := g.namedType(ti.Name)
	if !found || d == nil || d.decl == nil || d.instOrigin != nil || d.genericOf != nil {
		return nil, defaultHome{}, false
	}
	site, params, _, owning := g.siblingImplMemberBy(d, fa.Field.Name, "", nil)
	if site == nil || site.item.decl != fd || !site.accessorDefaults() {
		return nil, defaultHome{}, false
	}
	return params, site.defaultHome(owning), true
}

// instanceMemberHome is the parameter kinds and default home of fd when it
// is another file's impl function of a generic type's instance, the one
// the checker's signature for the call t names.
func (bl *irScalarBuilder) instanceMemberHome(t *ast.Call, fd *ast.FuncDef) ([]kind, defaultHome, bool) {
	g := bl.g
	ft := g.checkedCallSignature(t)
	if ft == nil {
		return nil, defaultHome{}, false
	}
	for _, ty := range append(append([]analysis.Type(nil), ft.Params...), ft.Return) {
		k := g.project(ty)
		if k.tag != tagNamed || k.def == nil {
			continue
		}
		src := k.def.instOrigin
		if src == nil {
			src = k.def
		}
		owner, d := g.instanceImplAnywhere(src, "", fd.Name)
		if d == nil || d.items[fd.Name] == nil || irImplSource(d.items[fd.Name]) != fd {
			continue
		}
		if owner == nil || owner == g {
			return nil, defaultHome{}, false
		}
		it := d.items[fd.Name]
		params := make([]kind, len(it.params))
		for i, p := range it.params {
			ip, imported := g.importKind(p)
			if !imported {
				return nil, defaultHome{}, false
			}
			params[i] = ip
		}
		return params, owner.instImplHome(d, it), true
	}
	return nil, defaultHome{}, false
}
