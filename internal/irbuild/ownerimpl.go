package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Owner functions: the functions of an inherent block on a generic owner
// that do not depend on the owner's type parameters.
//
// In `impl Box<T> { ... }` or `impl Iter<T> { ... }` the block's `T` is in
// scope, but a function is generic over it only when its signature names it.
// `fn seven() -> Int`, `fn twice<U>(x: U) -> #(U, U)` and `fn find<T>(source:
// Iter<T>)`, whose own `T` shadows the block's, name none of the block's
// parameters, so no call determines one, and the checker rejects a body that
// names one (analysis/owner_type_params.go). Such a function is an ordinary
// function the owner qualifies: compiled once, or once per tuple of its own
// type arguments, with no receiver.
//
// namesBlockParam is the one rule. The stdlib applies it when it files an
// impl function (collectStdCandidates: `Iter.from(start: Int)` is a
// monomorphic function, `Iter.find<T>` a generic one), and a user gen applies
// it here. A user gen otherwise builds an inherent block on a generic type
// once per instance (genericimpl.go), which a call with no instance operand
// (`Box.seven()`) cannot select, and builds no inherent block on an interface
// at all, since an interface is no receiver kind. So the owner functions of
// such a block are held apart, in a def with no receiver that is built once
// and reached by the owner's name (qualImplPlan, sameImplPlan), and the
// per-instance registration leaves them out. A function with its own type
// parameters is withheld from that def as any impl function's is, and each
// call instantiates it (genericMethodItem).

// namesBlockParam reports whether fd's signature or one of its `where`
// clauses names one of block, the type parameters an impl block's receiver
// contributes, that fd does not redeclare.
func namesBlockParam(fd *ast.FuncDef, block map[string]bool) bool {
	if len(block) == 0 {
		return false
	}
	free := block
	if len(fd.TypeParams) > 0 {
		free = make(map[string]bool, len(block))
		for n := range block {
			free[n] = true
		}
		for _, tp := range fd.TypeParams {
			delete(free, tp.Name)
		}
	}
	if sigNamesTypeParam(fd.Params, fd.ReturnTypeExpr, free) {
		return true
	}
	for _, wc := range fd.WhereClauses {
		if free[wc.Name] {
			return true
		}
	}
	return false
}

// ownerBlockParams is the type parameters of ib's receiver when ib is an
// inherent block whose owner is a generic type or an interface, the blocks
// whose owner functions this file holds apart.
func (g *gen) ownerBlockParams(ib *ast.ImplBlock) (map[string]bool, bool) {
	if ib.Interface != nil || isSynthesized(ib) {
		return nil, false
	}
	base := implReceiverBaseName(ib.Receiver)
	_, isIface := g.ifaces[base]
	if g.genericTemplates[base] == nil && !isIface {
		return nil, false
	}
	block := map[string]bool{}
	for _, n := range analysis.ReceiverTypeParamNames(ib.Receiver, g.declaresTypeName) {
		if n != "" {
			block[n] = true
		}
	}
	return block, true
}

// declaresTypeName reports whether name is a type here, which a receiver's
// type argument is when it is not one of the block's parameters. It reads
// declarations only, never a substitution frame, so a block is classified
// the same way inside an instance as outside one.
func (g *gen) declaresTypeName(name string) bool {
	switch name {
	case "Int", "Float", "String", "Bool", "Unit":
		return true
	}
	if _, isIface := g.ifaceNamed(name); isIface {
		return true
	}
	_, isType := g.namedType(name)
	return isType
}

// ownerFunc reports whether fd is an owner function of ib: a function of an
// inherent block on a generic type or an interface whose signature names
// none of the block's type parameters.
func (g *gen) ownerFunc(ib *ast.ImplBlock, fd *ast.FuncDef) bool {
	block, ok := g.ownerBlockParams(ib)
	return ok && !namesBlockParam(fd, block)
}

// registerOwnerImpl fills d with ib's owner functions and reports whether ib
// has any. A block with none keeps its ordinary registration.
func (g *gen) registerOwnerImpl(d *implDef, ib *ast.ImplBlock) bool {
	block, ok := g.ownerBlockParams(ib)
	if !ok {
		return false
	}
	has := false
	for _, item := range ib.Items {
		if fd, isFn := item.(*ast.FuncDef); isFn && !namesBlockParam(fd, block) {
			has = true
		}
	}
	if !has {
		return false
	}
	d.owner = implReceiverBaseName(ib.Receiver)
	_, isIface := g.ifaces[d.owner]
	for _, item := range ib.Items {
		fd, isFn := item.(*ast.FuncDef)
		if !isFn {
			continue
		}
		if namesBlockParam(fd, block) {
			if isIface {
				// An interface's block has no instance to build this for.
				d.noteGap(fd.Name, implGap{why: "inherent function over an interface's type parameter", detail: d.owner + "." + fd.Name})
			}
			// A generic type's instances build it (genericimpl.go).
			continue
		}
		it, gap := g.implItemSig(d, fd)
		if it == nil {
			// Nothing dispatches to an owner function, so a refusal is the
			// function's alone, whatever its unit.
			d.noteGap(fd.Name, gap)
			continue
		}
		d.items[it.name] = it
		d.order = append(d.order, it)
	}
	if g.ownerImpls == nil {
		g.ownerImpls = map[string][]*implDef{}
	}
	g.ownerImpls[d.owner] = append(g.ownerImpls[d.owner], d)
	return true
}

// ownerImplOf is the def holding ib's owner functions, or nil.
func (g *gen) ownerImplOf(ib *ast.ImplBlock) *implDef {
	if ib == nil || ib.Interface != nil {
		return nil
	}
	for _, d := range g.ownerImpls[implReceiverBaseName(ib.Receiver)] {
		if d.decl == ib {
			return d
		}
	}
	return nil
}

// ownerImplPlan resolves `Owner.method(args)` to an owner function: one item
// or a withheld generic function instantiated for this call. Two blocks that
// both supply the name decline, as typeQualifiedCall's ambiguity does.
func (bl *irScalarBuilder) ownerImplPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	var only *implItem
	supplied := false
	for _, d := range bl.g.ownerImpls[owner] {
		if d.supplies(method) {
			supplied = true
		}
		if !d.lowerable || !d.supplies(method) {
			continue
		}
		if only != nil {
			return nil
		}
		only = d.items[method]
		if only == nil {
			only = bl.genericMethodItem(t, d, method)
		}
		if only == nil {
			return nil
		}
	}
	if only == nil {
		if supplied {
			return nil
		}
		// No block of this file supplies it: another file's may.
		return bl.siblingOwnerPlan(t, args, owner, method)
	}
	return bl.implItemPlan(t, args, only, owner+"."+method)
}

// ownerDecl is the declaration of owner, a generic type or an interface this
// file declares, which keys its owner functions program-wide
// (fileIndex.ownerMembers), or nil.
func (g *gen) ownerDecl(owner string) ast.Node {
	if tpl := g.genericTemplates[owner]; tpl != nil && tpl.decl != nil {
		return tpl.decl
	}
	if d, isIface := g.ifaces[owner]; isIface && d != nil && d.decl != nil {
		return d.decl
	}
	return nil
}

// siblingOwnerPlan resolves `Owner.method(args)` to an owner function another
// file declares for Owner: `Box.of(1)` where boxes.nomi declares `Box<T>` and
// `impl Box<T> { pub fn of<U>(item: U) -> Box<U> }`. The owner is the
// declaration the analyzer resolved the name to, so an alias or a selective
// import reaches the same entry. A monomorphic one is called in the declaring
// unit, its kinds translated into this one as an impl function's are
// (qualSiblingIfaceImplPlan); a generic one is instantiated by the declaring
// unit for this call (siblingMethodPlan). One site only: two files supplying
// the name decline, as two local blocks do.
func (bl *irScalarBuilder) siblingOwnerPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	sym := resolvedTypeSymbol(bl.g.fa, owner)
	if q, name, dotted := strings.Cut(owner, "."); dotted {
		if scope := bl.g.moduleScope(q); scope != nil {
			// `boxes.Box.of(1)`: the owner the qualifier's file exports.
			sym = resolvedScopeSymbol(scope, name)
		}
	}
	if sym == nil || sym.Node == nil {
		return nil
	}
	return bl.siblingOwnerPlanFor(t, args, sym.Node, owner, method)
}

// siblingOwnerSites is the owner functions other files declare under method
// for the owner decl declares.
func (g *gen) siblingOwnerSites(decl ast.Node, method string) []implMemberSite {
	if g.files == nil || g.fileUnit < 0 || g.reg == nil || decl == nil {
		return nil
	}
	var sites []implMemberSite
	for _, s := range g.files.ownerMembers[implMemberKey{recv: decl, method: method}] {
		if s.unit != g.fileUnit {
			sites = append(sites, s)
		}
	}
	return sites
}

// siblingOwnerPlanFor is siblingOwnerPlan for the owner decl declares.
func (bl *irScalarBuilder) siblingOwnerPlanFor(t *ast.Call, args irQualArgs, decl ast.Node, owner, method string) *irQualPlan {
	g := bl.g
	sites := g.siblingOwnerSites(decl, method)
	if len(sites) != 1 {
		return nil
	}
	site := &sites[0]
	owning := g.reg.gens[site.unit]
	if owning == nil {
		return nil
	}
	if site.withheld != nil {
		if site.fn.why != "generic impl function" {
			// A private owner function, which the checker already refuses.
			return nil
		}
		return bl.siblingMethodPlan(t, args, owning, site.withheld, method)
	}
	f := site.fn
	if !f.lowerable() || site.item == nil {
		return nil
	}
	params := make([]kind, len(f.params))
	for i, p := range f.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil
		}
		params[i] = ip
	}
	result, ok := g.importKind(f.result)
	if !ok {
		return nil
	}
	var filled *irQualArgs
	if args.ok && len(args.temps) < len(params) {
		// `Box.pad("p")` omitting a parameter the declaring file defaults.
		if namedArgNode(t.Args) != nil {
			return nil
		}
		full, ok := bl.siblingDefaultArgs(t, args, site, params, owning)
		if !ok {
			return nil
		}
		filled = &full
		args = full
		t = &ast.Call{Func: t.Func, Args: make([]ast.Node, len(full.temps)), Line: t.Line, Col: t.Col}
	}
	if !bl.qualSignature(t, args, params, result) {
		return nil
	}
	p := &irQualPlan{token: f, name: g.files.units[site.unit].key + "." + owner + "." + method, result: result, args: filled}
	p.sym = owning.irCalleeSym(site.item, site.symName)
	return p
}

// siblingOwnerSpelling answers `mod.Owner` when qual spells it, mod qualifies
// another user file, and Owner is an interface or generic type for which
// another file declares an owner function named method: `boxes.Shape.twice(3)`.
// The three-segment route finds an owner by the dotted name of its mirror
// (qualDottedOwner), and an interface has none, so without this the call would
// be read as a call through a value. siblingOwnerPlan resolves the spelling.
func (bl *irScalarBuilder) siblingOwnerSpelling(qual ast.Node, method string) (string, bool) {
	inner, isFA := qual.(*ast.FieldAccess)
	if !isFA || inner.Field == nil {
		return "", false
	}
	mod, isIdent := inner.Object.(*ast.Ident)
	if !isIdent || irQualIsLocal(bl, mod.Name) {
		return "", false
	}
	scope := bl.g.moduleScope(mod.Name)
	if scope == nil {
		return "", false
	}
	sym := resolvedScopeSymbol(scope, inner.Field.Name)
	if sym == nil || sym.Node == nil || len(bl.g.siblingOwnerSites(sym.Node, method)) == 0 {
		return "", false
	}
	return mod.Name + "." + inner.Field.Name, true
}
