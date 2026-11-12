package irbuild

import (
	"github.com/nomi-language/nomi/internal/ir"
)

// A call into another FILE's impl function of a generic type's instance
// that omits a default the calling file cannot name: `Job.new("a", h)`
// against std/jobs's
//
//	impl Job<A> {
//	    pub fn new(kind: String, ..., timeout: Duration = Duration.minutes(5), handler: (A) -> ...)
//	}
//
// from a file that never imported `Duration`. A monomorphic impl's file
// builds an accessor per defaulted parameter of each `pub` member
// (irImplSlotDefaults), and a generic member with its own type parameters
// gets them per instance (methodInstAccessors). An instance's impl function
// is the third case: its defaults are lowered at the instance's type
// arguments, so the DECLARING gen builds them under the instance's frame,
// when a call first asks, as a field default's accessor is built
// (foreignfielddefault.go). Built only on request, so an instance nobody
// calls short builds nothing, and a default that does not lower is reported
// only at a program that reaches it.

// instImplAccessorReq is one instance impl function whose accessors a call
// asked for, with the frame its defaults are lowered under.
type instImplAccessorReq struct {
	d     *implDef
	it    *implItem
	inst  *genericImplInst
	name  string
	built bool
}

// instImplAccessors asks g, the gen that emits impl block d for a generic
// type's instance, for an accessor per defaulted parameter of its function
// it, named name as the call site names the function. It answers the
// function's callee symbol, or nil when g does not build d for an instance.
func (g *gen) instImplAccessorsFor(d *implDef, it *implItem, name string) *ir.Symbol {
	if d == nil || it == nil || it.decl == nil || it.inherited || len(it.params) != len(it.decl.Params) {
		return nil
	}
	if r := g.instImplAccessors[it]; r != nil {
		return g.irCalleeSym(it, r.name)
	}
	var inst *genericImplInst
	for _, e := range g.genericImplQueue {
		if e.impl == d {
			inst = e
			break
		}
	}
	if inst == nil {
		return nil
	}
	r := &instImplAccessorReq{d: d, it: it, inst: inst, name: name}
	if g.instImplAccessors == nil {
		g.instImplAccessors = map[*implItem]*instImplAccessorReq{}
	}
	g.instImplAccessors[it] = r
	g.instImplAccessorQueue = append(g.instImplAccessorQueue, r)
	return g.irCalleeSym(it, name)
}

// flushInstImplAccessors builds every queued request's accessors under its
// instance's frame. Building one can queue another, which the index loop
// picks up.
func (g *gen) flushInstImplAccessors() {
	for i := 0; i < len(g.instImplAccessorQueue); i++ {
		r := g.instImplAccessorQueue[i]
		if r.built {
			continue
		}
		r.built = true
		g.instImplAccessorsBuild(r)
	}
}

func (g *gen) instImplAccessorsBuild(r *instImplAccessorReq) {
	if r.inst.subst != nil {
		g.pushIfaceSubst(r.inst.subst)
		defer g.popIfaceSubst()
	} else {
		g.pushGenericSubst(r.inst.tpl, r.inst.args)
		defer g.popGenericSubst()
	}
	defer g.enterTypeScope(r.d.typeScope)()
	if r.d.ifaceSubst != nil {
		g.pushIfaceSubst(r.d.ifaceSubst)
		defer g.popIfaceSubst()
	}
	fd := r.it.decl
	callee := g.irCalleeSym(r.it, r.name)
	sig := &fnSig{params: r.it.params, result: r.it.result, decl: fd, lowerable: true}
	for slot, p := range fd.Params {
		if p.Default != nil {
			g.irFileSlotRetain(fd, sig, callee, r.name, slot)
		}
	}
}

// instImplAccessorsPending reports whether a request waits for its build.
func (g *gen) instImplAccessorsPending() bool {
	for _, r := range g.instImplAccessorQueue {
		if !r.built {
			return true
		}
	}
	return false
}

// instImplHome is where an instance impl function it of block d, which g
// emits, fills the parameters a call leaves out (fillCallDefaults): g's
// accessors for the instance (instImplAccessorsFor), asked for only when a
// default names what the calling file cannot.
func (g *gen) instImplHome(d *implDef, it *implItem) defaultHome {
	name := d.recv.nomi() + "." + it.name
	var callee *ir.Symbol
	asked := false
	return defaultHome{owner: g, accessor: func(slot int) *ir.Symbol {
		if !asked {
			callee, asked = g.instImplAccessorsFor(d, it, name), true
		}
		if callee == nil {
			return nil
		}
		return g.irFileSlotSym(callee, name, slot)
	}}
}
