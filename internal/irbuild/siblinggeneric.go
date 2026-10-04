package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A call to a generic `fn` another user file declares: `span.ident(4)`, or
// `ident(4)` after `import span.{ident}`.
//
// The instance is built where the declaration lives, by the declaring file's
// gen, as an instance of another file's generic TYPE is (foreigngeneric.go):
// the body resolves its names in its own file's scope, so only that file's gen
// can lower it. The call site reads the checker's instantiated signature in
// its own kinds, where a type parameter of an enclosing instance still
// resolves, and hands those kinds to the owner through importKind. The owner
// solves its template's type arguments against its own annotations and
// interns the instance in its own table (resolveMonoInstance), and the call
// site translates the instance's signature back, as siblingSignature does for
// a monomorphic `fn`. The call names the instance's symbol in the owner's
// table, which is how a cross-file call links (irSiblingCalleeSym).
//
// The owner may have finished its own module walk before a later file asks
// for an instance, so GenerateIR builds late instances once every walk is
// done (irFlushLateInstances).

// siblingGenericInstance resolves the call t to unit `to`'s generic `fn` f:
// the instance's parameter and result kinds in this gen, and its callee
// symbol. ok is false when the owner cannot build the instance.
func (g *gen) siblingGenericInstance(t *ast.Call, to int, f *fileFunc) ([]kind, kind, *ir.Symbol, bool) {
	no := func() ([]kind, kind, *ir.Symbol, bool) { return nil, kindInvalid, nil, false }
	if f == nil || !f.generic || f.decl == nil || g.reg == nil || to < 0 || to >= len(g.reg.gens) {
		return no()
	}
	owner := g.reg.gens[to]
	if owner == nil || owner == g {
		return no()
	}
	sig := owner.funcs[f.decl.Name]
	if sig == nil || sig.decl != f.decl {
		return no()
	}
	tpl := owner.irMonoTemplate(sig)
	if tpl == nil || len(t.Args) > len(tpl.decl.Params) {
		return no()
	}
	// A call may omit defaulted parameters or name its arguments: the
	// checker's instantiated signature has every parameter either way.
	here, result, ok := g.checkedMonoCallKinds(t, len(tpl.decl.Params))
	if !ok {
		return no()
	}
	return g.siblingInstanceAt(owner, tpl, f, here, result)
}

// siblingGenericRef is siblingGenericInstance for a REFERENCE to unit `to`'s
// generic `fn` f (`Iter.map(xs, span.ident)`): the instance the checker
// instantiated the reference at (funcref.go's checkedRefKinds).
func (g *gen) siblingGenericRef(ref ast.Node, to int, f *fileFunc) ([]kind, kind, *ir.Symbol, bool) {
	no := func() ([]kind, kind, *ir.Symbol, bool) { return nil, kindInvalid, nil, false }
	if f == nil || !f.generic || f.decl == nil || g.reg == nil || to < 0 || to >= len(g.reg.gens) {
		return no()
	}
	owner := g.reg.gens[to]
	if owner == nil || owner == g {
		return no()
	}
	sig := owner.funcs[f.decl.Name]
	if sig == nil || sig.decl != f.decl {
		return no()
	}
	tpl := owner.irMonoTemplate(sig)
	if tpl == nil {
		return no()
	}
	here, result, ok := g.checkedRefKinds(ref, len(tpl.decl.Params))
	if !ok {
		return no()
	}
	return g.siblingInstanceAt(owner, tpl, f, here, result)
}

// siblingInstanceAt asks owner for the instance of tpl at the parameter and
// result kinds here, which are this gen's, and translates its signature back.
func (g *gen) siblingInstanceAt(owner *gen, tpl *monoTemplate, f *fileFunc, here []kind, result kind) ([]kind, kind, *ir.Symbol, bool) {
	no := func() ([]kind, kind, *ir.Symbol, bool) { return nil, kindInvalid, nil, false }
	there := make([]kind, len(here))
	for i, k := range here {
		ik, ok := owner.importKind(k)
		if !ok {
			return no()
		}
		there[i] = ik
	}
	// kindInvalid: sentinel — an open result solves nothing on either side.
	if result != kindInvalid {
		ik, ok := owner.importKind(result)
		if !ok {
			return no()
		}
		result = ik
	}
	args, ok := owner.monoSolve(tpl, there, result)
	if !ok {
		return no()
	}
	// The chain is measured from the caller's instance, so two files whose
	// generics instantiate each other at growing arguments still meet
	// monoInstCap.
	prevDepth := owner.monoDepth
	owner.monoDepth = g.monoDepth
	inst, why, _ := owner.resolveMonoInstance(tpl, args, f.decl.Name)
	owner.monoDepth = prevDepth
	if why != "" || inst == nil {
		return no()
	}
	params := make([]kind, len(inst.sig.params))
	for i, p := range inst.sig.params {
		ip, ok := g.importKind(p)
		if !ok {
			return no()
		}
		params[i] = ip
	}
	res, ok := g.importKind(inst.sig.result)
	if !ok {
		return no()
	}
	return params, res, owner.irFunctionCallee(inst.sig), true
}

// siblingInstanceImplPlan resolves an impl call whose receiver d mirrors
// another file's generic instance (`Debug.inspect(span.Span{...})`, or
// `Display.to_string` over one): the impl the owner built for that instance,
// its signature imported here, called by the symbol the owner interned. The
// owner's gen lowers the body (flushGenericImpls), as it does for the
// instance's own uses.
func (bl *irScalarBuilder) siblingInstanceImplPlan(t *ast.Call, args irQualArgs, d *typeDef, method, iface string) *irQualPlan {
	g := bl.g
	owner, impl := g.instanceImpl(d, iface, method)
	if impl == nil || !impl.lowerable {
		return nil
	}
	it := impl.items[method]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != len(t.Args) {
		return nil
	}
	params := make([]kind, len(it.params))
	for i, p := range it.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil
		}
		params[i] = ip
	}
	result, ok := g.importKind(it.result)
	if !ok || !bl.qualSignature(t, args, params, result) {
		return nil
	}
	name := impl.recv.nomi() + "." + it.name
	return &irQualPlan{token: it, name: name, result: result, sym: owner.irCalleeSym(it, name)}
}
