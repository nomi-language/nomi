package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// An impl block whose receiver is a generic type ANOTHER file declares:
// `impl Holds for Wrap<T>` in impls.nomi over `pub struct Wrap<T>` in
// wrap.nomi. The orphan rule admits it (§13), and the checker's impl index
// answers a call through it wherever it is written.
//
// The block's body is written against its own file's scope, so its instances
// are built by its own file's gen, never the template owner's. An instance is
// minted in the owner's gen (genericInstance), so the two meet at the
// template: the owner records every instance whose impls it registered, the
// block's gen records itself on the template, and whichever arrives second
// registers the block for each instance at this gen's mirror of it, with the
// instance's type arguments in this gen's kinds. Dispatch then finds the
// block's implDef by the instance's identity in whichever gen holds it
// (instanceImplAnywhere).

// siblingTemplateImpl is an impl block of another file over a template.
type siblingTemplateImpl struct {
	g    *gen
	decl *ast.ImplBlock
}

// siblingInstKey is one block registered for one of the owner's instances.
type siblingInstKey struct {
	decl *ast.ImplBlock
	inst *typeDef
}

// siblingImplTemplate is the other file's template an impl block's receiver
// names, when it names one.
func (g *gen) siblingImplTemplate(ib *ast.ImplBlock) (*genericTemplate, bool) {
	gt, generic := ib.Receiver.(*ast.GenericType)
	if !generic || len(gt.Params) == 0 || ib.Interface == nil {
		return nil, false
	}
	if g.blockLocalTemplate(gt.Name) != nil || g.genericTemplates[gt.Name] != nil {
		return nil, false
	}
	ref, found := g.foreignTemplate(gt.Name)
	if !found || ref.tpl.why != "" {
		return nil, false
	}
	return ref.tpl, true
}

// registerSiblingTemplateImpl records ib, an impl over another file's
// template, as a template of this gen's per-instance impls, and registers it
// for every instance the owner has already registered impls for.
func (g *gen) registerSiblingTemplateImpl(d *implDef, ib *ast.ImplBlock, tpl *genericTemplate) {
	if !implBinderNamesTemplateParams(ib, tpl) || !implWhereNamesTemplateParams(ib, tpl) {
		d.lowerable, d.why, d.whyDetail = false, "generic impl block", typeText(ib.Receiver)
		return
	}
	d.lowerable, d.why, d.whyDetail = false, "generic type", typeText(ib.Receiver)
	d.siblingTemplate = tpl
	tpl.siblingImpls = append(tpl.siblingImpls, siblingTemplateImpl{g: g, decl: ib})
	for _, inst := range tpl.registered {
		g.registerSiblingInstanceImpl(tpl, ib, inst)
	}
}

// registerSiblingInstanceImpl registers ib for the owner's instance inst, at
// this gen's mirror of it, and queues its body. An instance this gen cannot
// name, or a block that declines at it, registers nothing, and a call through
// it declines at the call.
func (g *gen) registerSiblingInstanceImpl(tpl *genericTemplate, ib *ast.ImplBlock, inst *typeDef) {
	key := siblingInstKey{decl: ib, inst: inst}
	if g.siblingInstDone[key] {
		return
	}
	if g.siblingInstDone == nil {
		g.siblingInstDone = map[siblingInstKey]bool{}
	}
	g.siblingInstDone[key] = true
	recv, ok := g.importGenericInstance(inst)
	if !ok {
		return
	}
	args := make([]kind, len(inst.genericArgs))
	for i, a := range inst.genericArgs {
		k, ok := g.importKind(a)
		if !ok {
			return
		}
		args[i] = k
	}
	g.pushGenericSubst(tpl, args)
	id := g.registerImplAt(ib, recv)
	g.popGenericSubst()
	if !id.lowerable {
		return
	}
	g.genericImplQueue = append(g.genericImplQueue, &genericImplInst{impl: id, tpl: tpl, args: args})
}

// registerSiblingInstances is the owner's half: inst's impls are registered
// here, so every other file's block over the template is registered for it.
func (tpl *genericTemplate) registerSiblingInstances(inst *typeDef) {
	tpl.registered = append(tpl.registered, inst)
	for _, s := range tpl.siblingImpls {
		s.g.registerSiblingInstanceImpl(tpl, s.decl, inst)
	}
}

// instanceImplAnywhere is the impl of iface, or with iface empty the one impl
// that supplies method, registered for the owner's instance src in any gen:
// the owner's own blocks at src, and another file's at that file's mirror of
// src. Nil when there is none, or more than one.
func (g *gen) instanceImplAnywhere(src *typeDef, iface, method string) (*gen, *implDef) {
	if g.reg == nil || src == nil || src.genericOf == nil {
		return nil, nil
	}
	var og *gen
	var found *implDef
	for _, gg := range g.reg.gens {
		if gg == nil {
			continue
		}
		at := src
		if gg.genericInsts == nil || !gg.ownsInstance(src) {
			at = gg.foreignInstDefs[src]
			if at == nil {
				continue
			}
		}
		recv := named(at)
		for name, byRecv := range gg.implsByIface {
			if iface != "" && name != iface {
				continue
			}
			d := byRecv[recv]
			if d == nil || (method != "" && !d.supplies(method)) {
				continue
			}
			if found != nil && found != d {
				return nil, nil
			}
			og, found = gg, d
		}
	}
	return og, found
}

// instIdentity is a generic instance's identity: the owner's def, which a
// mirror of it names.
func instIdentity(d *typeDef) *typeDef {
	if d.instOrigin != nil {
		return d.instOrigin
	}
	return d
}

// ownsInstance reports whether src is one of this gen's own instances.
func (g *gen) ownsInstance(src *typeDef) bool {
	for _, d := range g.genericInsts[genericInstKey(src.genericOf, src.genericArgs)] {
		if d == src {
			return true
		}
	}
	return false
}
