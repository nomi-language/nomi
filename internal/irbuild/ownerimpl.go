package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Owner functions: the functions of an inherent block on a generic owner
// that do not depend on the owner's type parameters.
//
// In `impl Box<T> { ... }` or `impl Iter<T> { ... }` the block's `T` is in
// scope, but a function is generic over it only when its signature names it.
// `fn seven() -> Int`, `fn twice<U>(x: U) -> (U, U)` and `fn find<T>(source:
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
// and reached from the declaration a call's record names
// (recordedOwnerItem), and the per-instance registration leaves them out. A
// function with its own type parameters is withheld from that def as any
// impl function's is, and each call instantiates it (recordedWithheldPlan,
// genericMethodItem). A bare call from the same block is the
// type-qualified call (bareImplCall).

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
	if ib.Interface != nil {
		return nil, false
	}
	rec, _ := g.recordedTypeExpr(ib.Receiver)
	if dt, isDistinct := analysis.ResolveClosed(rec).(*analysis.DistinctType); isDistinct && dt.Refinement {
		// A distinct refinement has no receiver kind of its own, so its
		// functions, the generated ones included, are its owner's
		// (refinement.go).
		return map[string]bool{}, true
	}
	if isSynthesized(ib) {
		return nil, false
	}
	// The owner is the type the checker recorded for the receiver: this
	// file's generic template (receiverTemplate), or an interface.
	_, foreign, isTemplate := g.receiverTemplate(ib)
	_, isIface := analysis.ResolveClosed(rec).(*analysis.InterfaceType)
	if (!isTemplate || foreign) && !isIface {
		return nil, false
	}
	block := map[string]bool{}
	for _, n := range g.receiverTypeParamNames(ib) {
		if n != "" {
			block[n] = true
		}
	}
	return block, true
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
	_, isIface := g.receiverOwnerDecl(ib).(*ast.InterfaceDef)
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
		g.addImplItem(d, it)
	}
	if g.ownerImpls == nil {
		g.ownerImpls = map[string][]*implDef{}
	}
	g.ownerImpls[d.owner] = append(g.ownerImpls[d.owner], d)
	return true
}

// receiverOwnerDecl is the declaration the type the checker recorded for
// ib's receiver names (declNode): the generic type, interface or
// refinement whose owner functions ib holds, which keys them program-wide
// (fileIndex.ownerMembers), or nil for a receiver that names none.
func (g *gen) receiverOwnerDecl(ib *ast.ImplBlock) ast.Node {
	rec, _ := g.recordedTypeExpr(ib.Receiver)
	t := analysis.ResolveClosed(rec)
	var name string
	switch ty := t.(type) {
	case *analysis.StructType:
		name = ty.Name
	case *analysis.EnumType:
		name = ty.Name
	case *analysis.DistinctType:
		name = ty.Name
	case *analysis.InterfaceType:
		name = ty.Name
	default:
		return nil
	}
	n := g.declNode(originOfType(t), name)
	if isNilNode(n) {
		return nil
	}
	return n
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

// siblingOwnerPlanFor resolves `Owner.method(args)` to an owner function
// another file declares for the owner decl declares: `Box.of(1)` where
// boxes.nomi declares `Box<T>` and `impl Box<T> { pub fn of<U>(item: U) ->
// Box<U> }`. A monomorphic one is called in the declaring unit, its kinds
// translated into this one as an impl function's are
// (qualSiblingIfaceImplPlan); a generic one is instantiated by the
// declaring unit for this call (siblingMethodPlan). One site only: two
// files supplying the name decline, as two local blocks do.
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
	if args.ok && bl.g.leavesDefaults(t, len(params)) {
		// `Box.pad("p")` omitting a parameter the declaring file defaults.
		if namedArgNode(t.Args) != nil {
			return nil
		}
		full, ok := bl.filledOperands(t, args, params, site.fn.params0, site.defaultHome(owning))
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
	p.sym = owning.irImplSym(site.item)
	return p
}
