package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A generic interface implemented at several type arguments.
//
// `impl Contains<String> for Text` beside `impl Contains<Regex> for Text` are
// two blocks for one (interface, receiver) pair, told apart by their
// interface type arguments (implDef.instArgs). The front end chose one for
// each call (analysis' impl_selection.go) and recorded the chosen function on
// the call's method reference; every call lowers to a direct call of it. So
// such blocks never enter implsByIface, whose key is the pair: g.instImpls
// holds them, and each call route that finds no implsByIface entry asks
// pickInstImpl.

// ifaceTemplateArgs is a generic interface template's arguments under subst, in
// the template's parameter order.
func ifaceTemplateArgs(tpl *ast.InterfaceDef, subst map[string]kind) []kind {
	if tpl == nil || len(tpl.TypeParams) == 0 {
		return nil
	}
	out := make([]kind, len(tpl.TypeParams))
	for i, p := range tpl.TypeParams {
		k, bound := subst[p.Name]
		if !bound {
			return nil
		}
		out[i] = k
	}
	return out
}

// portableArgsKey spells type arguments by declaration, so every unit of a
// program spells one instantiation alike: a named type by its declaration
// node (a mirror shares its owner's) and its own arguments, a structural one
// by its parts.
func portableArgsKey(args []kind) string {
	var b strings.Builder
	for i, k := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		writePortableKind(&b, k)
	}
	return b.String()
}

func writePortableKind(b *strings.Builder, k kind) {
	fmt.Fprintf(b, "%d", k.tag)
	switch {
	case k.def != nil:
		d := instIdentity(k.def)
		if d.genericOf != nil && d.genericOf.decl != nil {
			fmt.Fprintf(b, "@%p", d.genericOf.decl)
		} else if d.decl != nil {
			fmt.Fprintf(b, "@%p", d.decl)
		} else {
			b.WriteString(":" + d.nomi)
		}
		if len(d.genericArgs) > 0 {
			b.WriteString("<" + portableArgsKey(d.genericArgs) + ">")
		}
	case k.iface != nil:
		fmt.Fprintf(b, "@%p", k.iface.decl)
		if len(k.iface.instArgs) > 0 {
			b.WriteString("<" + portableArgsKey(k.iface.instArgs) + ">")
		}
	case k.tp != nil:
		fmt.Fprintf(b, "@%p", k.tp)
	case k.comp != nil:
		b.WriteString("(" + strings.Join(k.comp.names, ",") + ":" + portableArgsKey(k.comp.parts) + ")")
	}
}

// ifaceInstLabel spells an instance for display: `Gets<String>`.
func ifaceInstLabel(d *ifaceDef) string {
	if len(d.instArgs) == 0 {
		return d.nomi
	}
	parts := make([]string, len(d.instArgs))
	for i, k := range d.instArgs {
		parts[i] = irKindName(k)
	}
	return d.nomi + "<" + strings.Join(parts, ", ") + ">"
}

// instMethodToken identifies a generic interface instance's method in its
// declaring unit's table: the template, the arguments and the method's
// name, the same in every unit, so a value boxed in one file dispatches to
// an impl recorded in another.
type instMethodToken struct {
	tpl    *ast.InterfaceDef
	args   string
	method string
}

// instMethodSym is the dispatch symbol of an instance's method m.
func (g *gen) instMethodSym(d *ifaceDef, m *ifaceMethod) *ir.Symbol {
	owning := g
	if d.foreign != "" {
		if g.reg == nil || d.unit < 0 || d.unit >= len(g.reg.gens) || g.reg.gens[d.unit] == nil {
			return nil
		}
		owning = g.reg.gens[d.unit]
	}
	token := instMethodToken{tpl: d.instOf, args: portableArgsKey(d.instArgs), method: m.name}
	return owning.irCalleeSym(token, ifaceInstLabel(d)+"."+m.name)
}

// ifaceInstTypeOf is the existential of a generic interface named at type
// arguments, `Gets<String>`: the template's instance at them.
func (g *gen) ifaceInstTypeOf(name string, params []ast.TypeExpr) (kind, bool) {
	if g.iterOwns(name) {
		// std's `Iter<T>` is the lowered sequence (structuralTypeOf).
		return kindInvalid, false
	}
	d, _ := g.ifaceNamed(name)
	tpl := g.genericTemplateDecl(d)
	if tpl == nil || len(params) != len(tpl.TypeParams) {
		return kindInvalid, false
	}
	subst := make(map[string]kind, len(params))
	for i, te := range params {
		k := g.typeOf(te)
		// kindInvalid: cascade — the argument's own position reports it.
		if k == kindInvalid {
			return kindInvalid, true
		}
		subst[tpl.TypeParams[i].Name] = k
	}
	inst := g.ifaceInstOf(tpl, subst)
	if inst == nil || !inst.def.lowerable {
		return kindInvalid, true
	}
	return existential(inst.def), true
}

// genericTemplateDecl is the declaration of the generic interface template
// d is, this file's or a mirror of another's, or nil.
func (g *gen) genericTemplateDecl(d *ifaceDef) *ast.InterfaceDef {
	if d == nil || d.decl == nil || d.instOf != nil || d.lowerable || d.why != "generic interface" {
		return nil
	}
	if len(d.decl.TypeParams) == 0 || len(d.decl.WhereClauses) > 0 {
		return nil
	}
	return d.decl
}

// ifaceInstOf is the template's instance at subst: instantiated here when
// its signatures mean the same here (ifaceTemplate), else mirrored from the
// declaring file's.
func (g *gen) ifaceInstOf(tpl *ast.InterfaceDef, subst map[string]kind) *ifaceInst {
	if g.ifaceTemplate(tpl.Name) == tpl {
		return g.ifaceInstAt(tpl, subst)
	}
	return g.foreignIfaceInst(tpl, subst)
}

// templateOwner is the declaring file of a generic interface another file
// of the program declares, or nil when this file declares it or there is
// no program.
func (g *gen) templateOwner(tpl *ast.InterfaceDef) *typeOwner {
	if g.reg == nil || tpl == nil {
		return nil
	}
	o := g.reg.byDecl[tpl]
	if o == nil || !o.isIface || o.unit == g.fileUnit || o.unit < 0 || o.unit >= len(g.reg.gens) || g.reg.gens[o.unit] == nil {
		return nil
	}
	return o
}

// foreignIfaceInst is this file's mirror of the declaring file's instance
// of a generic interface at subst (this file's kinds), for a template this
// file cannot instantiate itself: one it names nowhere, or whose signatures
// mean something else here. The declaring file instantiates it at the
// arguments as it sees them, and the mirror's method kinds are imported
// from that instance, as a mirror of an ordinary interface's are.
func (g *gen) foreignIfaceInst(tpl *ast.InterfaceDef, subst map[string]kind) *ifaceInst {
	o := g.templateOwner(tpl)
	if o == nil || !g.reg.ensureIfaces(o.unit) {
		return nil
	}
	args := ifaceTemplateArgs(tpl, subst)
	key := ifaceInstKey{tpl: tpl, args: portableArgsKey(args)}
	if inst := g.ifaceInsts[key]; inst != nil {
		return inst
	}
	owner := g.reg.gens[o.unit]
	ownerSubst := make(map[string]kind, len(subst))
	for name, k := range subst {
		ok := false
		if ownerSubst[name], ok = owner.importKind(k); !ok {
			return nil
		}
	}
	src := owner.ifaceInstAt(tpl, ownerSubst)
	if src == nil || !src.def.lowerable {
		return nil
	}
	d := &ifaceDef{
		nomi:      src.def.nomi,
		decl:      tpl,
		methods:   make(map[string]*ifaceMethod, len(src.def.order)),
		foreign:   o.fileKey,
		pkg:       o.pkg,
		unit:      o.unit,
		lowerable: true,
		instOf:    tpl,
		instArgs:  args,
	}
	inst := &ifaceInst{def: d, subst: subst}
	if g.ifaceInsts == nil {
		g.ifaceInsts = map[ifaceInstKey]*ifaceInst{}
	}
	g.ifaceInsts[key] = inst
	g.importIfaceMethods(d, src.def)
	return inst
}

// importIfaceInstKind is importIfaceKind for an instance of a generic
// interface: the instance at the same arguments, as this file names them.
func (g *gen) importIfaceInstKind(src *ifaceDef) (kind, bool) {
	tpl := src.instOf
	if len(src.instArgs) != len(tpl.TypeParams) {
		g.importWhy = "existential over a sibling file type"
		return kindInvalid, false
	}
	subst := make(map[string]kind, len(src.instArgs))
	for i, a := range src.instArgs {
		k, ok := g.importKind(a)
		if !ok {
			return kindInvalid, false
		}
		subst[tpl.TypeParams[i].Name] = k
	}
	var inst *ifaceInst
	if g.templateOwner(tpl) == nil {
		inst = g.ifaceInstAt(tpl, subst)
	} else {
		inst = g.ifaceInstOf(tpl, subst)
	}
	if inst == nil || !inst.def.lowerable {
		g.importWhy = "existential over a sibling file type"
		return kindInvalid, false
	}
	return existential(inst.def), true
}

// projectIfaceInst is the existential of a generic interface instantiation
// the checker solved, `Gets<String>` for an unannotated lambda parameter.
func (g *gen) projectIfaceInst(ty *analysis.InterfaceType) kind {
	d, found := g.solvedIface(ty.Origin, ty.Name)
	if !found {
		return kindInvalid
	}
	tpl := g.genericTemplateDecl(d)
	if tpl == nil || len(ty.TypeArgs) != len(tpl.TypeParams) {
		return kindInvalid
	}
	subst := make(map[string]kind, len(ty.TypeArgs))
	for i, a := range ty.TypeArgs {
		k := g.project(a)
		// kindInvalid: cascade — an argument the builder cannot name.
		if k == kindInvalid {
			return kindInvalid
		}
		subst[tpl.TypeParams[i].Name] = k
	}
	inst := g.ifaceInstOf(tpl, subst)
	if inst == nil || !inst.def.lowerable {
		return kindInvalid
	}
	return existential(inst.def)
}

func sameKinds(a, b []kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// registerInstImpl files d among its receiver's impls of a generic interface
// when the receiver implements that interface at several type arguments,
// and reports whether it did. A first impl stays in implsByIface; a second
// at other arguments moves both here.
func (g *gen) registerInstImpl(byRecv map[kind]*implDef, d *implDef) bool {
	if d.instArgs == nil {
		return false
	}
	byIface := g.instImpls[d.ifaceName]
	if held := byIface[d.recv]; len(held) > 0 {
		for _, o := range held {
			if sameKinds(o.instArgs, d.instArgs) {
				o.lowerable, o.why, o.whyDetail = false, "duplicate impl block", d.label()
				d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.label()
				return true
			}
		}
		byIface[d.recv] = append(held, d)
		return true
	}
	prior, held := byRecv[d.recv]
	if !held || prior.instArgs == nil || sameKinds(prior.instArgs, d.instArgs) {
		return false
	}
	delete(byRecv, d.recv)
	if g.instImpls == nil {
		g.instImpls = map[string]map[kind][]*implDef{}
	}
	if byIface == nil {
		byIface = map[kind][]*implDef{}
		g.instImpls[d.ifaceName] = byIface
	}
	byIface[d.recv] = []*implDef{prior, d}
	return true
}

// blockWrites reports whether impl is one of the block's own functions.
func blockWrites(b *ast.ImplBlock, impl ast.ImplMethodDecl) bool {
	if b == nil || impl == nil {
		return false
	}
	for _, item := range b.Items {
		if item == ast.Node(impl) {
			return true
		}
	}
	return false
}

// dispatchedImpl is the impl function the front end chose for a call, read
// off its method reference, or nil.
func (g *gen) dispatchedImpl(t *ast.Call) ast.ImplMethodDecl {
	fa, ok := t.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || g.fa == nil {
		return nil
	}
	sym := g.fa.References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return nil
	}
	return sym.DispatchImpl
}

// siblingInstPick chooses, among another file's impl functions of one
// interface for one receiver (its impls at several type arguments), the one
// the call t takes: the front end's choice, else the one whose parameter
// kinds take the operands, and then whose result is the call's.
func (bl *irScalarBuilder) siblingInstPick(t *ast.Call, args irQualArgs) func([]implMemberSite) *implMemberSite {
	return func(sites []implMemberSite) *implMemberSite {
		g := bl.g
		if impl := g.dispatchedImpl(t); impl != nil {
			for i := range sites {
				if fd, ok := impl.(*ast.FuncDef); ok && sites[i].fn != nil && sites[i].fn.decl == fd {
					return &sites[i]
				}
				if blockWrites(sites[i].block, impl) && sites[i].fn != nil && sites[i].fn.name == implMethodName(impl) {
					return &sites[i]
				}
			}
		}
		if !args.ok {
			return nil
		}
		var fits []*implMemberSite
		for i := range sites {
			f := sites[i].fn
			if f == nil || len(args.kinds) > len(f.params) {
				continue
			}
			ok := true
			for j, k := range args.kinds {
				p, imported := g.importKind(f.params[j])
				if !imported || (k != p && !g.irErases(p, k)) {
					ok = false
					break
				}
			}
			if ok {
				fits = append(fits, &sites[i])
			}
		}
		if len(fits) > 1 {
			// kindInvalid: lookup — no solved result leaves the choice open, and the call declines.
			if want := g.solvedCallReturn(t); want != kindInvalid {
				var byResult []*implMemberSite
				for _, s := range fits {
					if r, ok := g.importKind(s.fn.result); ok && r == want {
						byResult = append(byResult, s)
					}
				}
				fits = byResult
			}
		}
		if len(fits) != 1 {
			return nil
		}
		return fits[0]
	}
}

func implMethodName(impl ast.ImplMethodDecl) string {
	switch v := impl.(type) {
	case *ast.FuncDef:
		return v.Name
	case *ast.ExternFunc:
		return v.Name
	}
	return ""
}

// pickInstImpl selects, among one receiver's impls of a generic interface at
// several type arguments, the one the call t takes, and its item for method.
// The front end's choice decides when the call records one; otherwise (a
// bounded call in a generic instance, `C.contains?(c, x)` with C and T
// bound) the operands' kinds decide, and then the result's.
func (bl *irScalarBuilder) pickInstImpl(t *ast.Call, defs []*implDef, method string, args irQualArgs) (*implDef, *implItem) {
	item := func(d *implDef) *implItem {
		if d == nil || !d.lowerable {
			return nil
		}
		if it := d.items[method]; it != nil {
			return it
		}
		return bl.genericMethodItem(t, d, method)
	}
	if impl := bl.g.dispatchedImpl(t); impl != nil {
		for _, d := range defs {
			if blockWrites(d.decl, impl) {
				return d, item(d)
			}
		}
	}
	if !args.ok {
		return nil, nil
	}
	var fits []*implDef
	for _, d := range defs {
		it := item(d)
		if it == nil || len(args.kinds) > len(it.params) {
			continue
		}
		ok := true
		for i, k := range args.kinds {
			if k != it.params[i] && !bl.g.irErases(it.params[i], k) {
				ok = false
				break
			}
		}
		if ok {
			fits = append(fits, d)
		}
	}
	if len(fits) > 1 {
		// kindInvalid: lookup — no solved result leaves the choice open, and the call declines.
		if want := bl.g.solvedCallReturn(t); want != kindInvalid {
			var byResult []*implDef
			for _, d := range fits {
				if item(d).result == want {
					byResult = append(byResult, d)
				}
			}
			fits = byResult
		}
	}
	if len(fits) != 1 {
		return nil, nil
	}
	return fits[0], item(fits[0])
}
