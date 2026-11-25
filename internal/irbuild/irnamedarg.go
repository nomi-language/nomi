package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A qualified call with named arguments, `Time.new(minute: m, hour: h)`.
//
// The operands are evaluated in Nomi's order, as written (callPlan), and
// then the call is planned as the same call
// written positionally in parameter order, each operand a synthetic node
// that answers the temporary it was already evaluated into (irScalarBuilder.
// placed). So every qualified-call planner serves the named form unchanged.
//
// The parameter names come from the callee's declaration, which the checker
// resolved the field reference to. A slot the named arguments leave out
// before a supplied one takes its default before the call is planned.

// irPlacedArg is one operand a named-argument call already evaluated.
// written is the operand as written, which the stand-in stands for: the
// checker's records of the value it becomes where it is passed (a
// coercion into an interface value, a conversion) are keyed by it, and
// coercionAt and conversionAt read them there. nil for a slot a default
// filled, which has no written operand.
type irPlacedArg struct {
	temp    ir.Temp
	k       kind
	written ast.Node
}

// writtenAtSlot is the operand of t that fills slot by plan, nil when the
// slot is left to its default.
func writtenAtSlot(t *ast.Call, plan argPlan, slot int) ast.Node {
	for i, s := range plan.slots {
		if s == slot {
			return argExpr(t.Args[i])
		}
	}
	return nil
}

func (bl *irScalarBuilder) namedQualCall(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if fa.Field == nil || bl.g.fa == nil {
		panic("irbuild: namedQualCall: defensive: fa is set and the parser sets Field for a qualified call")
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return no()
	}
	if _, isType := fa.Object.(*ast.TypeIdent); isType {
		if route, _ := bl.g.recordedQualRoute(t, fa.Field.Name); route == routeStructUpdate {
			// `Struct.update(updates: {x: 1}, original: p)`, by the route its
			// record names: the patch must stay a literal the merge reads,
			// which a stand-in for an evaluated temporary is not, so the
			// update places its own arguments.
			if bl.recording > 0 {
				return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) { return bl.structUpdateCall(t) })
			}
			return bl.structUpdateCall(t)
		}
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
	for i, p := range params {
		names[i] = p.Name
	}
	plan, planned := bl.g.callPlan(t, len(params))
	if !planned {
		return no()
	}
	if len(t.Args) == 1 && len(names) == 1 && bl.recording == 0 {
		// `io.print(value: x)` is `io.print(x)`: one operand has one order.
		// The output primitives render their operand from its own node, so
		// the call keeps the written expression rather than a stand-in.
		positional := &ast.Call{Func: t.Func, Args: []ast.Node{argExpr(t.Args[0])}, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
		done := bl.g.carryCall(positional, bl.g.rewrittenCallOf(t, 1, 1))
		dst, k, mobile, ok := bl.hostOutput(positional, fa)
		done()
		if ok {
			return dst, k, mobile, true
		}
	}
	last := -1
	for _, s := range plan.slots {
		if s < 0 || s >= len(names) {
			panic("irbuild: namedQualCall: callPlan only yields slots inside the parameter list")
		}
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
	_, typeOwner := fa.Object.(*ast.TypeIdent)
	signalOwner := false
	if typeOwner && len(names) == 2 {
		_, key := bl.g.recordedQualRoute(t, fa.Field.Name)
		signalOwner = irSignallingCallback(key) && !irSeededIterMethod(key.Method)
	}
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
	gap := len(plan.defaulted) > 0 && plan.defaulted[0] < last
	if gap {
		// `Date.new(2026, day: 5)`: a slot omitted before a supplied one
		// takes its default before the call is planned, after the written
		// operands, as every omitted slot does (fillCallDefaults). The
		// planners then see every slot. An omitted slot's kind is the
		// function's the call reaches, when another file declares it, else
		// the checker's: the call's instantiated signature when it
		// recorded one (`other: Option<T> = None`), or the parameter's
		// declared type.
		rec := bl.g.callDefaultsOf(t)
		if rec == nil || len(rec.Params) != len(temps) {
			panic("irbuild: a call leaving out parameters with no CallDefaults record over its slots")
		}
		if f := bl.stdTemplateDefaults(fa, rec); f != nil {
			// A generic function of another std module, whose defaults
			// that module's accessors answer (stdslotfill.go).
			if !bl.fillStdTemplateDefaults(t, f, kinds, temps, plan.defaulted) {
				return no()
			}
			return bl.namedPlacedCall(t, fa, plan, names, temps, kinds, len(temps)-1)
		}
		home, params := bl.namedCallHome(t, rec, kinds, temps)
		if params != nil && len(params) != len(temps) {
			panic("irbuild: namedQualCall: namedCallHome's params are the declaration the CallDefaults record names, whose parameter count the record's Params has, and that was just checked against temps")
		}
		checked := bl.g.checkedCallSignature(t)
		if checked != nil && len(checked.Params) != len(temps) {
			checked = nil
		}
		omitted := plan.defaulted
		for _, i := range omitted {
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
		byAccessors := false
		for _, run := range defaultRuns(rec, home, omitted) {
			byAccessors = byAccessors || byAccessor(home, run.slots)
		}
		if !home.dispatched && byAccessors {
			// The declaring file's accessors take its parameter kinds: a
			// written operand of an interface-typed slot enters it by
			// erasure first, as a positional call's does
			// (qualEraseSlotted).
			for i, tmp := range temps {
				switch {
				case tmp == ir.NoTemp || kinds[i] == params[i]:
				case irHeldKind(params[i]):
					v, got, erased := bl.convertTo(writtenAtSlot(t, plan, i), tmp, kinds[i], params[i])
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
	return bl.namedPlacedCall(t, fa, plan, names, temps, kinds, last)
}

// namedPlacedCall plans the named-argument call t as the call written
// positionally over its slots up to last, each operand a stand-in for the
// temporary it was evaluated or filled into.
func (bl *irScalarBuilder) namedPlacedCall(t *ast.Call, fa *ast.FieldAccess, plan argPlan, names []string, temps []ir.Temp, kinds []kind, last int) (ir.Temp, kind, bool, bool) {
	args := make([]ast.Node, last+1)
	placed := make(map[ast.Node]irPlacedArg, len(args))
	for slot := range args {
		line, col := t.Line, t.Col
		written := writtenAtSlot(t, plan, slot)
		if written != nil {
			line, col = nodePos(written)
		}
		stand := &ast.Ident{Name: "\x00named", Line: line, Col: col}
		args[slot] = stand
		placed[stand] = irPlacedArg{temp: temps[slot], k: kinds[slot], written: written}
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
	// The stand-ins are written at their slots, in order.
	positional := &ast.Call{Func: t.Func, Args: args, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
	defer bl.g.carryCall(positional, bl.g.rewrittenCallOf(t, len(args), len(names)))()
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
// `Owner.f(...)` or `mod.Owner.f(...)` leaving a slot out, is declared,
// for fillCallDefaults, with its parameter kinds when the builder reaches
// it as another file's function: a sibling file's impl member, found by
// its declaration whatever the spelling, whose file builds accessors for
// its defaults (irImplSlotDefaults, implSlotHome), an impl function of
// another file's generic type's instance (instImplAccessorsFor), or, for
// a call of an interface function, the impl function its receiver
// reaches, this file's or another's, whose own defaults it takes, or the
// dispatch on an interface value.
func (bl *irScalarBuilder) namedCallHome(t *ast.Call, rec *analysis.DefaultedCall, kinds []kind, temps []ir.Temp) (defaultHome, []kind) {
	g := bl.g
	if fd, ok := rec.Decl.(*ast.FuncDef); ok && g.reg != nil && g.files != nil {
		if params, home, found := bl.siblingMemberHome(t, fd); found {
			return home, params
		}
		if params, home, found := bl.instanceMemberHome(t, fd); found {
			return home, params
		}
	}
	if im, ok := g.calleeDecl(t.Func).(*ast.InterfaceMethod); ok {
		if it := bl.reachedImplItem(im, kinds, temps); it != nil {
			fd := irImplSource(it)
			return defaultHome{params: fd.Params}, it.params
		}
		if home, ok := bl.dispatchedCallHome(im, kinds, temps); ok {
			return home, nil
		}
		if home, params, ok := bl.reachedImplHome(im, kinds, temps); ok {
			// Another file's impl function, whose accessors fill the
			// slots it defaults.
			return home, params
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
		impl := bl.g.implAt(bl.g.implsKey(d), recv)
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

// siblingMemberHome is the parameter kinds and default home of fd, the
// function the call t names, when it is a sibling file's inherent impl
// function of a monomorphic type. The type is the one the checker recorded
// fd is declared under (analysis.Callee.Owner), not the owner as t spells
// it, so `Box.f` after `import lib.{Box}` and `lib.Box.f` find one site.
func (bl *irScalarBuilder) siblingMemberHome(t *ast.Call, fd *ast.FuncDef) ([]kind, defaultHome, bool) {
	g := bl.g
	owner := g.routeCallee(t).Owner
	recv := g.qualifierDecl(owner)
	if owner.Kind != analysis.QualType || recv == nil || len(typeDeclTypeParams(recv)) > 0 {
		return nil, defaultHome{}, false
	}
	site, params, _, owning := g.siblingImplMemberBy(recv, fd.Name, "", nil)
	if site == nil || site.item.decl != fd {
		return nil, defaultHome{}, false
	}
	home := site.defaultHome(owning)
	if home.accessor == nil {
		return nil, defaultHome{}, false
	}
	return params, home, true
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
