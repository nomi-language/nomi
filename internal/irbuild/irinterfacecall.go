package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// erasedInterfacePlan dispatches on an erased receiver: the table keyed by
// the receiver's box, every self-typed operand opened with `.V`, and every
// other operand already the method's parameter kind. The receiver is a
// temporary like any operand, so an impure final receiver (an app-field
// read) needs no forcing. A sibling file's interface dispatches through the
// declaring unit's method symbol (irIfaceMethodSym), which is the key its
// impls are recorded under. Another interface's existential declines.
func (bl *irScalarBuilder) erasedInterfacePlan(t *ast.Call, args irQualArgs, d *ifaceDef, m *ifaceMethod) *irQualPlan {
	recvAt := m.shape.recvAt()
	var filled *irQualArgs
	if args.ok && len(args.kinds) < len(m.params) && m.decl != nil && len(m.decl.Params) == len(m.params) &&
		namedArgNode(t.Args) == nil && recvAt >= 0 && recvAt < len(args.kinds) {
		// `Named.greet(n)` omitting a defaulted parameter: the interface
		// declares the default, so every implementation receives the same
		// value, evaluated at the call as a direct call's default is.
		kinds := make([]kind, len(m.params))
		for i := range kinds {
			if m.shape.selfTyped(i) {
				kinds[i] = args.kinds[recvAt]
				continue
			}
			kinds[i] = m.params[i]
		}
		temps := make([]ir.Temp, len(kinds))
		copy(temps, args.temps)
		for i := len(args.temps); i < len(temps); i++ {
			temps[i] = ir.NoTemp
		}
		if !bl.callDefaults(&fnSig{decl: &ast.FuncDef{Name: m.name, Params: m.decl.Params}, params: kinds}, temps) {
			return nil
		}
		mobile := make([]bool, len(temps))
		copy(mobile, args.mobile)
		for i := len(args.mobile); i < len(mobile); i++ {
			mobile[i] = true
		}
		full := irQualArgs{temps: temps, kinds: kinds, mobile: mobile, ok: true}
		filled, args = &full, full
	}
	if !m.dispatchable() || len(args.kinds) != len(m.params) ||
		(!irCallableValueKind(m.result) && m.result != kindUnit) {
		return nil
	}
	sym := bl.g.irIfaceMethodSym(d, m)
	if sym == nil {
		return nil
	}
	payload := make([]bool, len(args.kinds))
	for i, k := range args.kinds {
		if m.shape.selfTyped(i) {
			if k.tag != tagIface || k.iface != d {
				return nil
			}
			payload[i] = true
			continue
		}
		if k != m.params[i] || !irCallOperandKind(k) {
			return nil
		}
	}
	return &irQualPlan{token: m, name: d.nomi + "." + m.name, result: m.result, dispatch: true, dispatchAt: recvAt, args: filled, sym: sym}
}

// concreteInterfacePlan uses the method's self position and the local
// implementation index. An erased receiver takes erasedInterfacePlan.
func (bl *irScalarBuilder) concreteInterfacePlan(t *ast.Call, args irQualArgs, d *ifaceDef, method string) *irQualPlan {
	m := d.methods[method]
	if !args.ok || (m == nil && !d.useSiteOnly) {
		return nil
	}
	recvAt := 0
	if m != nil {
		recvAt = m.shape.recvAt()
	}
	// A generic interface (`Announcer<T>`) records no dispatch table, but a
	// call whose first operand is a concrete implementing type reaches that
	// type's impl item exactly as a plain interface's does; the item's own
	// signature is checked against the operands below.
	if recvAt < 0 || recvAt >= len(args.kinds) {
		return nil
	}
	recv := args.kinds[recvAt]
	if recv.tag == tagIface {
		if m == nil {
			return nil
		}
		return bl.erasedInterfacePlan(t, args, d, m)
	}
	impl := bl.g.implsByIface[d.nomi][recv]
	if impl == nil {
		if p := bl.callerImplPlan(t, args, d.nomi, method, recv); p != nil {
			return p
		}
		if p := bl.stdIfacePlan(t, args, d.nomi, method); p != nil {
			return p
		}
		if recv.tag == tagNamed && recv.def != nil && d.decl != nil {
			// A sibling file's impl of this interface for a sibling type:
			// `Loud.say(things.quiet(3))`.
			return bl.qualSiblingIfaceImplPlan(t, args, recv.def, method, d.decl.Name)
		}
		return nil
	}
	if !impl.lowerable {
		return nil
	}
	it := impl.items[method]
	if it == nil {
		it = bl.genericMethodItem(t, impl, method)
	}
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil
	}
	var filled *irQualArgs
	if len(args.temps) < len(it.params) {
		// `Announcer.announce(b)` omitting a defaulted parameter.
		full, ok := bl.implDefaultArgs(t, args, it)
		if !ok {
			return nil
		}
		filled = &full
		args = full
		t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
	}
	if !bl.qualSignature(t, args, it.params, it.result) {
		return nil
	}
	return &irQualPlan{token: it, name: d.nomi + "." + method, result: it.result, args: filled}
}

// callerImplPlan is, inside a std generic instance built for a program, the
// program's own impl of iface for a receiver the program declares:
// `Maybe.hash<Written>`'s derived body calls `Hashable.hash(w)`, and the impl
// is the user's `impl Hashable for Written`. The instance interns the
// program's kinds in the calling gen's tables, so the receiver is the kind
// that gen's impl index is keyed by; the call names the impl's body by the
// symbol its declaring gen interned.
func (bl *irScalarBuilder) callerImplPlan(t *ast.Call, args irQualArgs, iface, method string, recv kind) *irQualPlan {
	s := bl.g.stdInsts
	if s == nil || s.caller == nil || s.caller == bl.g || bl.g.stdModule == "" ||
		recv.tag != tagNamed || recv.def == nil || recv.def.decl == nil {
		return nil
	}
	var og *gen
	var d *implDef
	gens := []*gen{s.caller}
	if s.caller.reg != nil {
		gens = append(gens, s.caller.reg.gens...)
	}
	for _, g := range gens {
		if g == nil {
			continue
		}
		if impl := g.implsByIface[iface][recv]; impl != nil {
			og, d = g, impl
			break
		}
	}
	if d == nil && recv.def.foreign != "" {
		// `io.print(a.make())` where `a.Point` is declared, and its Display
		// implemented, in another file: the caller holds a MIRROR of Point,
		// and the impl is registered in the declaring file's gen against the
		// declaration's own def. The two share the declaration node.
		og, d = mirroredImpl(gens, iface, recv.def)
		if recv.def.instOrigin != nil {
			// A generic instance: every instantiation shares that node,
			// so the owner's instance def names the impl.
			og, d = s.caller.instanceImpl(recv.def, iface, method)
		}
	}
	if d == nil || !d.lowerable {
		return nil
	}
	it := d.items[method]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != len(args.kinds) {
		return nil
	}
	params, result := it.params, it.result
	if og != s.caller {
		// The declaring gen's kinds, named in the caller's tables, which are
		// the kinds this instance's operands carry.
		params = make([]kind, len(it.params))
		for i, p := range it.params {
			ip, ok := s.caller.importKind(p)
			if !ok {
				return nil
			}
			params[i] = ip
		}
		r, ok := s.caller.importKind(it.result)
		if !ok {
			return nil
		}
		result = r
	}
	if !bl.qualSignature(t, args, params, result) {
		return nil
	}
	return &irQualPlan{token: it, name: iface + "." + method, result: result, sym: og.irCalleeSym(it, d.recv.nomi()+"."+it.name)}
}

// mirroredImpl finds the impl of iface registered for the declaration a
// mirror def names, in the gen that declares it.
func mirroredImpl(gens []*gen, iface string, mirror *typeDef) (*gen, *implDef) {
	if mirror.decl == nil {
		return nil, nil
	}
	for _, g := range gens {
		if g == nil {
			continue
		}
		for recv, impl := range g.implsByIface[iface] {
			if recv.def != nil && recv.def.foreign == "" && recv.def.decl == mirror.decl {
				return g, impl
			}
		}
	}
	return nil, nil
}

// foreignIfacePlan selects this module's impl of an interface declared
// elsewhere, `ToJson.to_json(user)` for a derived `impl ToJson for User`.
// Only a single exact fit is planned.
func (bl *irScalarBuilder) foreignIfacePlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	byRecv, known := bl.g.implsByIface[owner]
	if !known || owner == "" || !args.ok || bl.g.iterOwns(owner) {
		return nil
	}
	for _, k := range args.kinds {
		if k.tag == tagIface {
			return nil
		}
	}
	var chosen *implItem
	impls := bl.g.implOrder
	if r := args.kinds[0]; r.tag == tagNamed && r.def != nil && r.def.genericOf != nil {
		// `Display.to_string(b)` over a `Box<Int>`: an impl on a generic
		// receiver is registered per instance (registerInstanceImpls) and
		// implOrder does not hold it.
		impls = append(bl.g.implsOf(r), impls...)
	}
	for _, d := range impls {
		if d.ifaceName != owner || !d.lowerable || byRecv[d.recv] != d {
			continue
		}
		it := d.items[method]
		if it == nil || !bl.qualSignature(t, args, it.params, it.result) {
			continue
		}
		if chosen != nil {
			return nil
		}
		chosen = it
	}
	if chosen == nil || !chosen.lowerable || irImplSource(chosen) == nil {
		return nil
	}
	return &irQualPlan{token: chosen, name: owner + "." + method, result: chosen.result}
}

// stdIfacePlan selects a stdlib interface method by its first argument's kind
// and plans the call to its retained body or approved runtime host.
//
// A written std body's call records the call's line as its Go source cursor.
func (bl *irScalarBuilder) stdIfacePlan(t *ast.Call, args irQualArgs, iface, method string) *irQualPlan {
	g := bl.g
	spelling := iface + "." + method
	if g.std == nil || !args.ok || len(args.kinds) == 0 || len(g.std.byType[spelling]) != 0 {
		return nil
	}
	byRecv, isIface := g.std.byIface[spelling]
	if !isIface {
		return nil
	}
	f := stdPick(byRecv[args.kinds[0]], args.kinds)
	p := bl.stdFuncPlan(t, args, f)
	if p == nil {
		p = bl.shadowedIfacePlan(t, args, f)
	}
	return p
}

// shadowedIfacePlan plans an interface-qualified call to a std impl that an
// inherent method of the same spelling shadows (`Display.to_string(bytes)`
// beside the inherent `Bytes.to_string`). The shadow decides what
// `Bytes.to_string` names, and an interface qualifier names the impl
// unambiguously; its retained body has its own declaration symbol.
func (bl *irScalarBuilder) shadowedIfacePlan(t *ast.Call, args irQualArgs, f *stdFunc) *irQualPlan {
	if f == nil || f.why != shadowedByInherent || f.iface == "" {
		return nil
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irBody == nil {
		return nil
	}
	if !bl.qualSignature(t, args, f.params, f.result) {
		return nil
	}
	return &irQualPlan{token: f, name: f.iface + "." + f.name, result: f.result, sym: f.irBody.Sym()}
}
