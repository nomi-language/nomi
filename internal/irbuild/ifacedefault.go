package irbuild

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// AN INTERFACE DEFAULT, LOWERED IN ITS DECLARING FILE.
//
// `fn describe(s: self) -> String { label(area(s)) }` in shape.nomi is Nomi
// written in shape.nomi: `label` may be that file's private function, and
// the body may name its imports and its `once` values. So the body is
// lowered by the declaring file's gen, once per implementing type, with
// `self` bound to that type (gen.defaultSelf), wherever the implementing
// block is. An implementing block in the declaring file lowers the default
// as one of its own functions (inheritDefaults, implFunc), which is the
// same scope. A block in another file keeps its item, so every route that
// names the block's function (a type-qualified call, the dispatch table, a
// dictionary, a function value) names the same symbol as before, and the
// item's body is one tail call of the declaring file's instance
// (irImplForwardDefault). The instance's own calls of the interface's
// functions at the receiver (`area(s)`, `Shape.describe(s)`) are the
// block's items (defaultInstImplPlan), so an `open` override the block
// writes is the one called, and a default the block inherits is its
// forwarding item again.
//
// An instance is keyed by the default's declaration, the receiver and the
// type arguments the body is lowered at (the interface's, a generic default's
// own), all in the declaring gen's kinds, and links by the interface
// function's linkage name with those arguments
// (`shape.Shape.describe<Square>`).

// ifaceDefaultInst is one default of an interface this file declares,
// instantiated for one implementing type of another file.
type ifaceDefaultInst struct {
	iface *ifaceDef
	m     *ifaceMethod
	recv  kind
	// frame binds the interface's type parameters and the default's own,
	// in this gen's kinds; nil when there are none.
	frame map[string]kind
	// impl is the implementing block, impls the gen that registered it:
	// what a call of the interface's function at the receiver reaches.
	impl   *implDef
	impls  *gen
	fd     *ast.FuncDef
	params []kind
	result kind
	built  bool
}

// irImplForwardDefault lowers inherited default it of block d, whose
// interface another file declares, as a tail call of that file's instance at
// d's receiver.
func (g *gen) irImplForwardDefault(d *implDef, it *implItem, fd *ast.FuncDef, params []kind) (kind, bool) {
	callee, why := g.defaultForwardCallee(d, it, params)
	// The forwarding function sits at the block, the declaration in this
	// file that gave the receiver the default.
	var at ast.Node = fd
	if d.decl != nil {
		at = d.decl
	}
	name := d.ownerName() + "." + it.name
	sig := irFuncSig{result: it.result, decl: fd, name: name, origin: irFromImpl}
	var sh *irFuncShell
	if callee != nil {
		if sh = g.irFuncShellAt(at, fd.Params, sig, params, g.irCalleeSym(it, name)); sh == nil {
			why = "an interface default whose result has no IR type: " + it.result.nomi()
		}
	}
	if why != "" {
		g.dc.note(why)
		return kindInvalid, false
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: it.result, sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}, vmOnly: true}
	pos := g.irNodePos(at)
	call := ir.NewCall(pos, sh.fn.NewTemp(), ir.TailCall, callee, sh.paramTemps...)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: it.result})
	cp := ir.NewCopy(pos, sh.result, call.Dst())
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: it.result})
	bl.b.SetTerm(ir.NewReturn(pos, sh.result))
	if err := g.irLint(sh.fn); err != nil {
		panic("irbuild: interface default forwarding function: " + err.Error())
	}
	g.irModule().AddFunc(sh.fn)
	if err := g.irLintAdded(); err != nil {
		panic("irbuild: interface default forwarding function: " + err.Error())
	}
	if irFuncObserved != nil {
		irFuncObserved(sig.origin, sig.name, sh.fn, true)
	}
	return it.result, true
}

// defaultForwardCallee is the symbol of the declaring file's instance of
// inherited default it at block d's receiver, whose signature in this gen's
// kinds is params and it.result, or the reason there is none.
func (g *gen) defaultForwardCallee(d *implDef, it *implItem, params []kind) (*ir.Symbol, string) {
	m := d.iface.methods[it.name]
	owner := g.unitGen(d.iface.unit)
	if m == nil || m.decl == nil || owner == nil || owner == g {
		return nil, "an interface default whose declaring file is not in the program: " + d.ifaceName + "." + it.name
	}
	cannot := "an interface default its declaring file cannot instantiate: " + d.ifaceName + "." + it.name + " for " + d.recv.nomi()
	recv, ok := owner.importKind(d.recv)
	if !ok {
		return nil, cannot
	}
	// The interface's type parameters and the default's own, bound by the
	// frames the block (emitImpl) and a generic default's instance
	// (flushMethodInsts) pushed.
	frame := map[string]kind{}
	var tps []ast.TypeParam
	if d.iface.decl != nil {
		tps = append(tps, d.iface.decl.TypeParams...)
	}
	tps = append(tps, m.decl.TypeParams...)
	for _, tp := range tps {
		k, bound := g.genericSubstKind(tp.Name)
		ik, ok := owner.importKind(k)
		if !bound || !ok {
			return nil, cannot
		}
		frame[tp.Name] = ik
	}
	inst := owner.ifaceDefaultInstance(d.iface, m.decl, recv, frame, d, g)
	if inst == nil || len(inst.params) != len(params) {
		return nil, cannot
	}
	for i, p := range inst.params {
		if ip, ok := g.importKind(p); !ok || ip != params[i] {
			return nil, cannot
		}
	}
	if r, ok := g.importKind(inst.result); !ok || r != it.result {
		return nil, cannot
	}
	return owner.irCalleeSym(inst, inst.iface.nomi+"."+inst.m.name), ""
}

// ifaceDefaultInstance is this file's instance of the default declared by
// im, of the interface another file's block impl (registered by impls)
// implements through the mirror, at recv and frame, which are this gen's
// kinds. It is nil when the default's signature is outside the subset
// there. The body is queued for flushIfaceDefaults.
func (g *gen) ifaceDefaultInstance(mirror *ifaceDef, im *ast.InterfaceMethod, recv kind, frame map[string]kind, impl *implDef, impls *gen) *ifaceDefaultInst {
	iface := g.declaredIface(mirror, frame)
	if iface == nil {
		return nil
	}
	m := iface.methods[im.Name]
	if m == nil || m.decl != im {
		return nil
	}
	body, ok := im.Body.(*ast.Block)
	if !ok {
		return nil
	}
	key := fmt.Sprintf("%p\x00%s\x00%s", im, recv.key(), frameKey(frame))
	if inst := g.ifaceDefaults[key]; inst != nil {
		return inst
	}
	inst := &ifaceDefaultInst{iface: iface, m: m, recv: recv, impl: impl, impls: impls}
	if len(frame) > 0 {
		inst.frame = frame
		g.pushIfaceSubst(frame)
		defer g.popIfaceSubst()
	}
	prevSelf := g.defaultSelf
	g.defaultSelf = &irDefaultSelf{iface: iface, recv: recv}
	defer func() { g.defaultSelf = prevSelf }()
	inst.params = make([]kind, len(im.Params))
	for i, p := range im.Params {
		k := g.paramKind(p)
		if m.shape.selfTyped(i) {
			k = recv
		}
		if !irCallableValueKind(k) {
			return nil
		}
		inst.params[i] = k
	}
	inst.result = kindUnit
	if im.ReturnTypeExpr != nil {
		inst.result = g.typeOf(im.ReturnTypeExpr)
		if inst.result != kindUnit && !irCallableValueKind(inst.result) {
			return nil
		}
	}
	inst.fd = &ast.FuncDef{
		Name:           im.Name,
		Params:         im.Params,
		ReturnTypeExpr: im.ReturnTypeExpr,
		Body:           body,
		Line:           im.Line,
		Col:            im.Col,
	}
	if g.ifaceDefaults == nil {
		g.ifaceDefaults = map[string]*ifaceDefaultInst{}
	}
	g.ifaceDefaults[key] = inst
	g.ifaceDefaultQueue = append(g.ifaceDefaultQueue, inst)
	return inst
}

// declaredIface is this file's own def of the interface another file's
// mirror stands for: its declaration, or for an instance of a generic
// interface the instance at the arguments in frame.
func (g *gen) declaredIface(mirror *ifaceDef, frame map[string]kind) *ifaceDef {
	if mirror == nil {
		return nil
	}
	if tpl := mirror.instOf; tpl != nil {
		if src := g.ifaces[tpl.Name]; src == nil || src.decl != tpl || src.foreign != "" {
			return nil
		}
		return g.ifaceInstAt(tpl, frame).def
	}
	if mirror.decl == nil {
		return nil
	}
	src := g.ifaces[mirror.decl.Name]
	if src == nil || src.decl != mirror.decl || src.foreign != "" || !src.lowerable {
		return nil
	}
	return src
}

// frameKey spells a substitution frame, its names sorted.
func frameKey(frame map[string]kind) string {
	names := make([]string, 0, len(frame))
	for n := range frame {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "=" + frame[n].key() + "\x00")
	}
	return b.String()
}

// ifaceDefaultLink is an instance's linkage name: the interface function's,
// with the receiver and then the frame's arguments in name order.
func (g *gen) ifaceDefaultLink(inst *ifaceDefaultInst) string {
	args := []kind{inst.recv}
	names := make([]string, 0, len(inst.frame))
	for n := range inst.frame {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		args = append(args, inst.frame[n])
	}
	return within(g.declLink(inst.m.decl), "<"+portableArgsKey(args)+">")
}

// flushIfaceDefaults builds every queued instance's body. A worklist: a body
// may reach a further instance.
func (g *gen) flushIfaceDefaults() {
	for i := 0; i < len(g.ifaceDefaultQueue); i++ {
		inst := g.ifaceDefaultQueue[i]
		if inst.built {
			continue
		}
		inst.built = true
		g.emitIfaceDefault(inst)
	}
}

// ifaceDefaultsPending reports whether an instance is queued and unbuilt.
func (g *gen) ifaceDefaultsPending() bool {
	for _, inst := range g.ifaceDefaultQueue {
		if !inst.built {
			return true
		}
	}
	return false
}

// emitIfaceDefault lowers one instance's body in this file's scope, with
// `self` and the frame bound, as implFunc lowers an impl function.
func (g *gen) emitIfaceDefault(inst *ifaceDefaultInst) {
	g.at(inst.fd.Line)
	prevTail, prevEmitting, prevBlock := g.tail, g.implEmitting, g.implBlock
	prevSelf, prevInst, prevResult := g.defaultSelf, g.defaultInst, g.result
	g.tail, g.implEmitting, g.implBlock = nil, nil, nil
	g.defaultSelf = &irDefaultSelf{iface: inst.iface, recv: inst.recv}
	g.defaultInst = inst
	g.result = inst.result
	defer func() {
		g.tail, g.implEmitting, g.implBlock = prevTail, prevEmitting, prevBlock
		g.defaultSelf, g.defaultInst, g.result = prevSelf, prevInst, prevResult
	}()
	if inst.frame != nil {
		g.pushIfaceSubst(inst.frame)
		defer g.popIfaceSubst()
	}
	g.pushScope()
	defer g.popScope()
	for i, p := range inst.fd.Params {
		if p.Destructure == nil && !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: inst.params[i]})
		}
	}
	name := inst.iface.nomi + "." + inst.m.name
	g.irBodyObserve(irBodyImplFn, true)
	g.dc.take()
	sig := irFuncSig{result: inst.result, decl: inst.fd, name: name, origin: irFromImpl}
	sh := g.irFuncShellWithCallee(inst.fd, sig, inst.params, g.irCalleeSym(inst, name))
	g.irImplPrologue(inst.fd, sh, inst.params)
	produced, retained := g.irScalarLower(inst.fd, sig, nil, sh, inst.result)
	if !retained {
		g.unloweredBody(g.dc.take())
		return
	}
	if produced != inst.result {
		g.reject("impl return type mismatch",
			fmt.Sprintf("%s for %s declares %s and returns %s", name, inst.recv.nomi(), inst.result.nomi(), produced.nomi()), inst.fd.Body)
	}
}

// defaultInstImplPlan plans `Iface.method(args)` of the instance being
// lowered's own interface at its receiver: the implementing block's item,
// which the block's gen lowers. It is nil for any other call.
func (bl *irScalarBuilder) defaultInstImplPlan(t *ast.Call, args irQualArgs, d *ifaceDef, method string, recv kind) *irQualPlan {
	inst := bl.g.defaultInst
	if inst == nil || d == nil || inst.iface == nil || d.decl != inst.iface.decl || recv != inst.recv || inst.impl == nil {
		return nil
	}
	it := inst.impl.items[method]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil
	}
	params := make([]kind, len(it.params))
	for i, p := range it.params {
		ip, ok := bl.g.importKind(p)
		if !ok {
			return nil
		}
		params[i] = ip
	}
	result, ok := bl.g.importKind(it.result)
	if !ok || !bl.qualSignature(t, args, params, result) {
		return nil
	}
	name := inst.impl.ownerName() + "." + it.name
	return &irQualPlan{token: it, name: d.nomi + "." + method, result: result, sym: inst.impls.irCalleeSym(it, name)}
}

// ownDefaultCall reports whether the bare callee, in the body of a default
// being instantiated, names a function of that default's own interface: the
// call is that function at the receiver.
func ownDefaultCall(fa *analysis.FileAnalysis, callee *ast.Ident, ds *irDefaultSelf) bool {
	sym := resolveSymbol(fa.References[analysis.Pos{Line: callee.Line, Col: callee.Col}])
	if sym == nil {
		sym = resolvedBareSymbolAt(fa, callee)
	}
	return sym != nil && sym.Kind == analysis.SymbolInterfaceMethod && sym.OwningInterface == ds.iface.nomi
}
