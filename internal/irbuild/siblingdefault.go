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
	owner, isIdent := ast.QualifierIdent(fa.Object)
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

// irFileSlotDefaults builds the accessors of a `fn` another file may call: a
// `pub fn`, or a private one when the file's partner test is in the program.
// Called from funcDecl, so an instance of a generic template gets its own
// with the template's type arguments in scope.
func (g *gen) irFileSlotDefaults(fd *ast.FuncDef, sig *fnSig) {
	if g.irMod == nil || g.files == nil || sig == nil || (!fd.Public && !g.hasPartnerTest()) || fd.ImplFunction ||
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
	// A member with type parameters of its own is lowered per instance, and
	// an instance's accessors are built when a call asks for them, under the
	// instance's frame and the name the call gives (methodInstSlotDefaults).
	// Building them here too would define each accessor twice.
	if len(fd.TypeParams) > 0 {
		return
	}
	if def := d.recv.def; d.recv.tag != tagNamed || def == nil || def.instOrigin != nil || def.genericOf != nil {
		return
	}
	if _, host := hostExtOf(fd); host {
		return
	}
	// The body's symbol as irImplLower interns it, and the accessors' name as
	// a call site's defaultHome gives it (implMemberSite.symName).
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

// irFileSlotRetain builds the accessor for one defaulted parameter: the
// parameters before slot, then that parameter's default lowered as a local
// call's is (callDefaults), seeing this file's functions and the earlier
// parameters. A default that does not lower leaves no accessor, and a program
// that reaches one is reported BLOCKED under the accessor's name, at the
// default.
func (g *gen) irFileSlotRetain(fd *ast.FuncDef, sig *fnSig, callee *ir.Symbol, fn string, slot int) {
	g.irSlotAccessorRetain(fd, sig, g.irFileSlotSym(callee, fn, slot), fileSlotName(fn, slot), slot)
}

// irSlotAccessorRetain builds sym, named name, as the accessor for slot of
// fd: irFileSlotRetain's body, for any accessor symbol (ifaceslotdefault.go).
func (g *gen) irSlotAccessorRetain(fd *ast.FuncDef, sig *fnSig, sym *ir.Symbol, name string, slot int) {
	g.irDeclineOpen(name)
	defer g.dc.atNode(fd.Params[slot].Default)
	// The accessor's shell holds its parameters and its result, the
	// defaulted parameter's kind, to the value domain (irFuncShellAt).
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
	sh := g.irFuncShellWithCallee(&short, isig, params, sym)
	if sh == nil {
		g.dc.note("no function shell")
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
	if !bl.callDefaults(&fnSig{decl: &filled, params: sig.params[:slot+1], ifaceFrame: sig.ifaceFrame}, args) {
		g.dc.note("a default that does not lower")
		return
	}
	at := g.irNodePos(fd)
	cp := ir.NewCopy(at, sh.result, args[slot])
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: sig.params[slot]})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	g.irRequireLint(sh.fn, "a default accessor")
	g.irModule().AddFunc(sh.fn)
	if err := g.irLintAdded(); err != nil {
		panic("irbuild: file default accessor: " + err.Error())
	}
}

// defaultHome is where a call of site, a sibling file's impl function
// whose unit's gen is owning, fills the parameters it leaves out
// (fillCallDefaults): the accessors owning builds for the function
// (irImplSlotDefaults), or for the instance of a withheld member
// (methodInstAccessors), and none for any other.
func (site *implMemberSite) defaultHome(owning *gen) defaultHome {
	home := defaultHome{owner: owning}
	switch {
	case site.accessorDefaults():
		callee := owning.irCalleeSym(site.item, site.symName)
		home.accessor = func(slot int) *ir.Symbol { return owning.irFileSlotSym(callee, site.symName, slot) }
	case site.instAccessors != nil:
		var callee *ir.Symbol
		asked := false
		home.accessor = func(slot int) *ir.Symbol {
			if !asked {
				callee, asked = site.instAccessors(), true
			}
			if callee == nil {
				return nil
			}
			return owning.irFileSlotSym(callee, site.symName, slot)
		}
	}
	return home
}
