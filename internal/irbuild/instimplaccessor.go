package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
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

// namedInstanceImplFill fills the slots a named-argument call to another
// file's impl function of a generic type's instance leaves out, `Job.new("k",
// max_attempts: 3, send)`, with the declaring gen's accessors when a default
// names what this file cannot. The instance is the one the checker's signature
// for the call names. handled is false when the callee is no such function, or
// its defaults lower here; ok is whether the slots were filled.
func (bl *irScalarBuilder) namedInstanceImplFill(t *ast.Call, fd *ast.FuncDef, kinds []kind, temps []ir.Temp) (ok, handled bool) {
	g := bl.g
	if g.reg == nil || fd == nil {
		return false, false
	}
	ft := g.checkedCallSignature(t)
	if ft == nil {
		return false, false
	}
	var owner *gen
	var impl *implDef
	var it *implItem
	for _, ty := range append(append([]analysis.Type(nil), ft.Params...), ft.Return) {
		k := g.project(ty)
		if k.tag != tagNamed || k.def == nil {
			continue
		}
		src := k.def.instOrigin
		if src == nil {
			src = k.def
		}
		o, d := g.instanceImplAnywhere(src, "", fd.Name)
		if d == nil || d.items[fd.Name] == nil || irImplSource(d.items[fd.Name]) != fd {
			continue
		}
		owner, impl, it = o, d, d.items[fd.Name]
		break
	}
	if it == nil || owner == nil || owner == g || len(it.params) != len(temps) {
		return false, false
	}
	var slots []int
	for i, tmp := range temps {
		if tmp == ir.NoTemp {
			slots = append(slots, i)
		}
	}
	if bl.siblingDefaultsAgree(&fileFunc{name: fd.Name, params0: fd.Params}, owner, slots) {
		return false, false
	}
	params := make([]kind, len(it.params))
	for i, p := range it.params {
		ip, imported := g.importKind(p)
		if !imported {
			return false, true
		}
		params[i] = ip
	}
	for i, tmp := range temps {
		if tmp == ir.NoTemp {
			kinds[i] = params[i]
		}
	}
	name := impl.recv.nomi() + "." + it.name
	callee := owner.instImplAccessorsFor(impl, it, name)
	if callee == nil {
		return false, true
	}
	return bl.siblingFill(t, owner, callee, instImplDecl(fd, name), params, temps, nil), true
}

// instImplDecl is the declaration of an instance impl function, as
// siblingFill reads its parameters, under the name the accessors carry.
func instImplDecl(fd *ast.FuncDef, name string) *ast.FuncDef {
	return &ast.FuncDef{Name: name, Params: fd.Params}
}
