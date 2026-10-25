package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A call into another FILE that omits a defaulted parameter:
// `lib.add(1)` against `pub fn add(a: Int, b: Int = 1) -> Int`, or `add(1)`
// after `import lib.add`.
//
// A default is Nomi source belonging to the file that declared it. It may name
// that file's private functions and types the caller never imported
// (`mode: Mode = Mode.ReadWrite`), so it is lowered in the DECLARING file's gen,
// never at the call site: the declaring unit builds one accessor per
// defaulted parameter of each `pub fn`, taking the parameters before it and
// answering that parameter's default (irFileSlotRetain). That is std's rule
// for its own defaults (stdslotfill.go), for the same reason.
//
// The caller evaluates every written argument, then fills each omitted slot in
// ascending order with a call to its accessor over the slots below it, then
// calls the function at full arity. Ascending order means an accessor always
// has its prefix in hand, which is what lets a default name an earlier
// parameter (`b: Int = a * 2`).
//
// A generic `fn`'s instance gets its own accessors: the declaring unit builds
// them while it emits the instance, with the instance's type arguments in
// scope, so a default whose type mentions `T` has a concrete type.

// irFileSlotDefault identifies one accessor: the function or instance it
// belongs to, by that function's own callee symbol, and the parameter slot.
type irFileSlotDefault struct {
	callee *ir.Symbol
	slot   int
}

// fileSlotName is the accessor's name, which a BLOCKED line prints when the
// declaring unit could not build it.
func fileSlotName(fn string, slot int) string {
	return fmt.Sprintf("%s default %d", fn, slot+1)
}

// irFileSlotSym is the accessor symbol for slot of callee, interned in g's
// table. g is the DECLARING unit's gen, whichever unit asks.
func (g *gen) irFileSlotSym(callee *ir.Symbol, fn string, slot int) *ir.Symbol {
	return g.irCalleeSym(irFileSlotDefault{callee: callee, slot: slot}, fileSlotName(fn, slot))
}

// siblingQualSite resolves the callee `owner.name` of a qualified call to a
// sibling file's `fn`, by the file the analyzer bound owner to. ok is false
// when owner is a local, names no sibling file, or the file declares no such
// function.
func (bl *irScalarBuilder) siblingQualSite(fa *ast.FieldAccess) (fileSite, bool) {
	g := bl.g
	owner, isIdent := fa.Object.(*ast.Ident)
	if !isIdent || fa.Field == nil || g.files == nil || irQualIsLocal(bl, owner.Name) {
		return fileSite{}, false
	}
	to, isSibling := g.files.lookupQualifier(g.fa, owner)
	if !isSibling || to == g.fileUnit {
		return fileSite{}, false
	}
	f := g.files.units[to].funcs[fa.Field.Name]
	if f == nil || f.decl == nil {
		return fileSite{}, false
	}
	_, host := hostExtOf(f.decl)
	return fileSite{unit: to, fn: f, host: host}, true
}

// unitGen is unit to's gen, or nil when there is no program registry.
func (g *gen) unitGen(to int) *gen {
	if g.reg == nil || to < 0 || to >= len(g.reg.gens) {
		return nil
	}
	return g.reg.gens[to]
}

// irFileSlotDefaults builds the accessors of a `pub fn` another file may call.
// Called from funcDecl, so an instance of a generic template gets its own
// with the template's type arguments in scope.
func (g *gen) irFileSlotDefaults(fd *ast.FuncDef, sig *fnSig) {
	if g.irMod == nil || g.files == nil || sig == nil || !fd.Public || fd.ImplFunction ||
		len(sig.params) != len(fd.Params) || sig.dict != nil || sig.tps != nil {
		return
	}
	callee := g.irFunctionCallee(sig)
	for slot, p := range fd.Params {
		if p.Default != nil {
			g.irFileSlotRetain(fd, sig, callee, fd.Name, slot)
		}
	}
}

// irImplSlotDefaults builds the accessors of a `pub` function of an inherent
// impl block another file may call, `Fake.new()` against `pub fn new(dialect:
// Dialect = Dialect.Other("fake"))`, for a call whose file cannot name what
// the defaults name (siblingAccessorArgs). A generic receiver's members, an
// interface's impl functions and a Go-bound function build none.
func (g *gen) irImplSlotDefaults(d *implDef, it *implItem) {
	fd := it.decl
	if g.irMod == nil || g.files == nil || d.iface != nil || d.ifaceName != "" || it.inherited ||
		fd == nil || !fd.Public || len(it.params) != len(fd.Params) || !it.lowerable {
		return
	}
	if def := d.recv.def; d.recv.tag != tagNamed || def == nil || def.instOrigin != nil || def.genericOf != nil {
		return
	}
	if _, host := hostExtOf(fd); host {
		return
	}
	// The body's symbol as irImplLower interns it, and the accessors' name as
	// a call site's siblingFill gives it (implMemberSite.symName).
	callee := g.irCalleeSym(it, d.ownerName()+"."+it.name)
	name := d.recv.nomi() + "." + it.name
	sig := &fnSig{params: it.params, result: it.result, decl: fd, lowerable: true}
	for slot, p := range fd.Params {
		if p.Default != nil {
			g.irFileSlotRetain(fd, sig, callee, name, slot)
		}
	}
}

// accessorDefaults reports whether site's declaring file builds an accessor
// for each of its defaulted parameters (irImplSlotDefaults).
func (site *implMemberSite) accessorDefaults() bool {
	it := site.item
	return site.iface == "" && !site.synth && site.withheld == nil && it != nil && !it.inherited &&
		it.decl != nil && it.decl.Public
}

// siblingAccessorArgs fills the slots a short call to site, a sibling file's
// inherent impl function, omits, with a call to the accessor its file builds
// for each (irImplSlotDefaults): the default may name that file's private
// functions and types, which this file cannot lower. args holds the written
// operands, in order from the first slot.
func (bl *irScalarBuilder) siblingAccessorArgs(t *ast.Call, args irQualArgs, site *implMemberSite, params []kind, owning *gen) (irQualArgs, bool) {
	if !site.accessorDefaults() || len(args.temps) > len(params) {
		return args, false
	}
	temps := make([]ir.Temp, len(params))
	mobile := make([]bool, len(params))
	copy(temps, args.temps)
	copy(mobile, args.mobile)
	for i := len(args.temps); i < len(temps); i++ {
		temps[i] = ir.NoTemp
	}
	if !bl.siblingImplFill(t, site, owning, params, temps, mobile) {
		return args, false
	}
	return irQualArgs{temps: temps, kinds: append([]kind(nil), params...), mobile: make([]bool, len(temps)), ok: true}, true
}

// siblingImplFill is siblingFill against site, a sibling file's inherent impl
// function whose accessors its file builds.
func (bl *irScalarBuilder) siblingImplFill(at ast.Node, site *implMemberSite, owning *gen, params []kind, temps []ir.Temp, mobile []bool) bool {
	callee := owning.irCalleeSym(site.item, site.symName)
	decl := &ast.FuncDef{Name: site.symName, Params: site.fn.params0}
	return bl.siblingFill(at, owning, callee, decl, params, temps, mobile)
}

// namedSiblingImplFill fills the slots a named-argument call to a sibling
// file's inherent impl function leaves out, `Fake.new(answer: f)`, with its
// file's accessors when a default names what this file cannot
// (siblingDefaultsAgree). handled is false when the callee is not such a
// function, or its defaults lower here; ok is whether the slots were filled.
func (bl *irScalarBuilder) namedSiblingImplFill(t *ast.Call, fa *ast.FieldAccess, fd *ast.FuncDef, kinds []kind, temps []ir.Temp) (ok, handled bool) {
	g := bl.g
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType || fa.Field == nil || g.files == nil || g.reg == nil {
		return false, false
	}
	d, found := g.namedType(ti.Name)
	if !found || d == nil || d.decl == nil || d.instOrigin != nil || d.genericOf != nil {
		return false, false
	}
	site, params, _, owning := g.siblingImplMemberBy(d, fa.Field.Name, "", nil)
	if site == nil || site.item.decl != fd || !site.accessorDefaults() || len(params) != len(temps) {
		return false, false
	}
	var slots []int
	for i, tmp := range temps {
		if tmp == ir.NoTemp {
			slots = append(slots, i)
		}
	}
	if bl.siblingDefaultsAgree(site.fn, owning, slots) {
		return false, false
	}
	for i, tmp := range temps {
		if tmp == ir.NoTemp {
			kinds[i] = params[i]
		} else if kinds[i] != params[i] {
			return false, true
		}
	}
	return bl.siblingImplFill(t, site, owning, params, temps, nil), true
}

// irFileSlotRetain builds the accessor for one defaulted parameter: the
// parameters before slot, then that parameter's default lowered as a local
// call's is (callDefaults), seeing this file's functions and the earlier
// parameters. A default that does not lower leaves no accessor, and a program
// that reaches one is reported BLOCKED under the accessor's name, at the
// default.
func (g *gen) irFileSlotRetain(fd *ast.FuncDef, sig *fnSig, callee *ir.Symbol, fn string, slot int) {
	name := fileSlotName(fn, slot)
	g.irDeclineOpen(name)
	defer irDeclineAtNode(fd.Params[slot].Default)
	for _, k := range sig.params[:slot+1] {
		if !irCallOperandKind(k) {
			irDeclineNote("a parameter kind outside the call domain: " + k.nomi())
			return
		}
	}
	g.pushScope()
	defer g.popScope()
	params := sig.params[:slot]
	for i, p := range fd.Params[:slot] {
		if p.Destructure == nil && !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: params[i]})
		}
	}
	short := *fd
	short.Params = fd.Params[:slot]
	isig := irFuncSig{result: sig.params[slot], decl: fd, name: name, origin: irFromModule}
	sh := g.irFuncShellWithCallee(&short, isig, params, g.irFileSlotSym(callee, fn, slot))
	if sh == nil {
		irDeclineNote("no function shell")
		return
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: sig.params[slot], sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	args := make([]ir.Temp, slot+1)
	for i, p := range fd.Params[:slot] {
		args[i] = sh.params[p.Name]
	}
	args[slot] = ir.NoTemp
	filled := *fd
	filled.Params = fd.Params[:slot+1]
	if !bl.callDefaults(&fnSig{decl: &filled, params: sig.params[:slot+1]}, args) {
		irDeclineNote("a default that does not lower")
		return
	}
	at := g.irNodePos(fd)
	cp := ir.NewCopy(at, sh.result, args[slot])
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: sig.params[slot]})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	if err := g.irLint(sh.fn); err != nil {
		irDeclineNote("a default accessor that does not lint: " + err.Error())
		return
	}
	g.irModule().AddFunc(sh.fn)
	if err := g.irLintAdded(); err != nil {
		panic("irbuild: file default accessor: " + err.Error())
	}
}

// siblingFill completes a call's operand vector against a sibling file's
// function: temps holds every written operand at its parameter slot and
// ir.NoTemp at each omitted one. Each omitted slot becomes a call to the
// accessor the declaring unit `owner` builds for it (irFileSlotRetain), in
// ascending order, after every written operand is held, so the written
// arguments run first and each default runs once. callee is the function's
// own symbol in owner's table. ok is false when an omitted slot has no
// default.
func (bl *irScalarBuilder) siblingFill(at ast.Node, owner *gen, callee *ir.Symbol, decl *ast.FuncDef, params []kind, temps []ir.Temp, mobile []bool) bool {
	omitted := false
	for _, t := range temps {
		if t == ir.NoTemp {
			omitted = true
		}
	}
	if !omitted {
		return true
	}
	if owner == nil || callee == nil || decl == nil || len(decl.Params) != len(temps) || len(params) != len(temps) {
		return false
	}
	pos := bl.g.irNodePos(at)
	for i, t := range temps {
		if t == ir.NoTemp || (i < len(mobile) && mobile[i]) || irHeldValue(bl.f, t, bl.sides) {
			continue
		}
		cp := ir.NewCopy(pos, bl.f.NewTemp(), t)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[i], copy: irCopyForce})
		temps[i] = cp.Dst()
	}
	for slot := range temps {
		if temps[slot] != ir.NoTemp {
			continue
		}
		if decl.Params[slot].Default == nil || !irCallOperandKind(params[slot]) {
			return false
		}
		sym := owner.irFileSlotSym(callee, decl.Name, slot)
		c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, temps[:slot]...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: params[slot]})
		cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[slot], copy: irCopyForce})
		temps[slot] = cp.Dst()
	}
	return true
}
