package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A GENERIC INTERFACE, monomorphized per instantiation.
//
// `interface Gets<T>` is a template: its def stays unlowerable, and nothing
// dispatches through it. Each instantiation the program reaches is an
// ordinary `*ifaceDef` built by `resolveIface` over the same declaration
// with the arguments' substitution frame pushed (`g.genericSubst`, which
// typeOf's `*ast.SimpleType` arm and `project()` read), interned by the
// template and its arguments (ifaceInstKey):
//
//   - IMPLEMENTED. `impl Gets<String> for Bag` supplies ground arguments,
//     read off the header when it writes them and solved from the item
//     signatures when it does not, and binds into the instance at them.
//     A receiver may implement one template at several arguments, each a
//     block of its own (instimpl.go).
//   - ERASED. `h: Gets<String>` is the existential of the instance at
//     `String`, whose method tables are its own: an impl at `Gets<Int>`
//     binds into another instance, so a value held as either dispatches to
//     the impl at its own arguments. Another file's instance is named by the
//     template and portable arguments (instMethodToken), so every file
//     dispatches through the declaring file's tables.
//
// What stays refused:
//
//   - An interface-level `where` clause with no type parameters. It is a
//     different construct, a constraint on `self`, and no arm here reads it.
//   - A template whose type parameters cannot ALL be solved from the impl
//     block. Reported as `generic interface` at the impl, naming the parameter
//     that stayed open.
//   - A generic interface FUNCTION (`fn choose<K>(…) where K: Comparable`).
//     `methodSignatureGap` reports `generic interface function`: the function's
//     OWN type parameter is bound by the call site rather than the impl block
//     (see ifaceMethodTypeParamIsOpen).
//   - An interface VARIANT or FIELD requirement on a generic interface. The
//     other arms report; nothing here reorders them.

// ifaceInst is one generic interface instantiated at ground type arguments,
// carrying the frame its method signatures and default BODIES both need.
//
// The frame travels with the work rather than being re-derived, for
// `genericImplInst`'s stated reason: `flushGenericImpls` and `emitImpl` run long
// after the registering frame was popped, and a default body is exactly where
// `T` has to still resolve.
type ifaceInst struct {
	def *ifaceDef
	// subst is the template's type parameters bound to ground kinds. Never nil
	// and never empty for an instance, which is what distinguishes it from an
	// ordinary def whose `ifaceSubst` is nil.
	subst map[string]kind
}

// ifaceInstKey is one instantiation's identity: the template and its type
// arguments (portableArgsKey). Every impl of `Gets<String>` binds into one
// def, whose method symbols are the dispatch table a value held as
// `Gets<String>` is called through, and `Gets<Int>` is another.
type ifaceInstKey struct {
	tpl  *ast.InterfaceDef
	args string
}

// ifaceTemplate is the *ast.InterfaceDef of a generic interface declared in this
// file or, when its signatures mean the same here, in a sibling file, or nil.
//
// Read off the interface's def rather than kept in a second map: the template
// def already carries the declaration and the refusal every unreached mention
// reports. A parallel registry would be a second place for "which interfaces
// are generic" to be answered.
func (g *gen) ifaceTemplate(name string) *ast.InterfaceDef {
	d, _ := g.ifaceNamed(name)
	if d == nil || d.lowerable || d.instOf != nil {
		return nil
	}
	if d.why != "generic interface" || d.decl == nil {
		return nil
	}
	// Exactly the shape declareIfaces put the marker on: type parameters, and
	// no interface-level where clause. Re-derived from the DECLARATION rather
	// than trusted from the marker, so a future arm that reuses the same `why`
	// for a different shape cannot silently be admitted here.
	if len(d.decl.TypeParams) == 0 || len(d.decl.WhereClauses) > 0 {
		return nil
	}
	if d.foreign != "" && !g.portableSignatures(d) {
		return nil
	}
	return d.decl
}

// portableSignatures reports whether a sibling file's generic interface can
// be instantiated here: its functions' signatures are resolved in this file,
// so every name they mention must resolve to the same declaration in both
// files, the rule a default lowered at its call is held to
// (defaultNamesAgree).
func (g *gen) portableSignatures(d *ifaceDef) bool {
	if g.reg == nil || d.unit < 0 || d.unit >= len(g.reg.gens) {
		return false
	}
	owner := g.reg.gens[d.unit]
	if owner == nil || owner.fa == nil || g.fa == nil {
		return false
	}
	names := map[string]bool{}
	for i := range d.decl.Methods {
		im := &d.decl.Methods[i]
		for j := range im.Params {
			walkNames(im.Params[j].TypeAnnotation, names, pickAnyName)
		}
		walkNames(im.ReturnTypeExpr, names, pickAnyName)
	}
	for name := range names {
		there, here := resolvedTypeSymbol(owner.fa, name), resolvedTypeSymbol(g.fa, name)
		switch {
		case there == nil && here == nil:
			// A type parameter, `self`, or a name neither file binds.
		case there == nil || here == nil || there.Node != here.Node:
			return false
		}
	}
	return true
}

// ifaceInstanceFor is the instance an impl block of a generic interface binds
// into, or a refusal naming what stayed open.
//
// The block's type arguments are solved under the frame its receiver was
// registered at, so one block on a generic receiver (`impl Chooser for
// Pair<E>`, registered once per instantiation) reaches `Chooser<Int>` for
// `Pair<Int>` and `Chooser<Priority>` for `Pair<Priority>`
// (`11-interfaces-and-impls/type_argument_inference`).
func (g *gen) ifaceInstanceFor(tpl *ast.InterfaceDef, ib *ast.ImplBlock) (*ifaceInst, string, string) {
	subst, why, detail := g.ifaceInstArgs(tpl, ib)
	if why != "" {
		return nil, why, detail
	}
	return g.ifaceInstAt(tpl, subst), "", ""
}

// ifaceInstAt is the template instantiated at subst, interned by its
// arguments: the def every impl at those arguments binds into and every
// value held as that instantiation is erased to.
func (g *gen) ifaceInstAt(tpl *ast.InterfaceDef, subst map[string]kind) *ifaceInst {
	args := ifaceTemplateArgs(tpl, subst)
	key := ifaceInstKey{tpl: tpl, args: portableArgsKey(args)}
	if inst := g.ifaceInsts[key]; inst != nil {
		return inst
	}
	d := &ifaceDef{
		nomi:      tpl.Name,
		decl:      tpl,
		methods:   map[string]*ifaceMethod{},
		lowerable: true,
		instOf:    tpl,
		instArgs:  args,
		unit:      -1,
	}
	if m := g.ifaces[tpl.Name]; m != nil && m.decl == tpl && m.foreign != "" {
		// A sibling file's template: an inherited default is lowered in
		// that file (ifacedefault.go), which it reaches through these.
		d.foreign, d.pkg, d.unit = m.foreign, m.pkg, m.unit
	} else if o := g.templateOwner(tpl); o != nil {
		d.foreign, d.pkg, d.unit = o.fileKey, o.pkg, o.unit
	}
	if t := g.stdProgIfaces[tpl]; t != nil {
		// A std generic interface's instance (stdprogiface.go).
		d.stdOrigin = t.stdOrigin
	}
	inst := &ifaceInst{def: d, subst: subst}
	// Interned BEFORE resolving, so a default body that reaches this interface
	// again resolves against the same def rather than building a second one.
	if g.ifaceInsts == nil {
		g.ifaceInsts = map[ifaceInstKey]*ifaceInst{}
	}
	g.ifaceInsts[key] = inst
	g.pushIfaceSubst(subst)
	g.resolveIface(d)
	g.popIfaceSubst()
	return inst
}

// ifaceInstArgs is the template's type arguments for the impl block.
//
// TWO SOURCES, and the explicit one wins because it is what the programmer
// wrote:
//
//	impl Add<Score, Score> for Score      // header, positional
//	impl Announcer for Box { … }          // from the item signatures
//
// The second is the checker's answer: the type builder solved the
// interface's parameters from the items' signatures against the
// interface's and recorded them on the block (analysis.ImplBlockTypeArgs),
// and they are projected here in the frame the receiver was registered at.
// A parameter no item names (the record's Open), or one with no record, is
// a refusal naming it.
func (g *gen) ifaceInstArgs(tpl *ast.InterfaceDef, ib *ast.ImplBlock) (map[string]kind, string, string) {
	names := make([]string, 0, len(tpl.TypeParams))
	for _, tp := range tpl.TypeParams {
		names = append(names, tp.Name)
	}
	subst := map[string]kind{}

	if gt, explicit := ifaceHeaderArgs(ib.Interface); explicit {
		if len(gt) != len(names) {
			return nil, "generic interface impl", typeText(ib.Interface)
		}
		for i, te := range gt {
			k := g.typeOf(te)
			// kindInvalid: cascade — the type argument's own position reports it.
			if k == kindInvalid {
				return nil, "generic interface", tpl.Name + "<" + typeText(te) + ">"
			}
			subst[names[i]] = k
		}
		return subst, "", ""
	}

	rec := analysis.ImplBlockTypeArgs(g.srcFA(), ib)
	if rec == nil && g.fa != g.srcFA() {
		rec = analysis.ImplBlockTypeArgs(g.fa, ib)
	}
	for i, n := range names {
		// The parameter that stayed open, named. A bare `generic interface`
		// here would send the reader to the DECLARATION, which is fine, and
		// would not say which of `<K, V>` the impl failed to pin.
		if rec == nil || i >= len(rec.Args) || (i < len(rec.Open) && rec.Open[i]) {
			return nil, "generic interface", tpl.Name + " type parameter " + n
		}
		k := g.project(rec.Args[i])
		// kindInvalid: cascade — an argument the builder cannot name.
		if k == kindInvalid {
			return nil, "generic interface", tpl.Name + " type parameter " + n
		}
		subst[n] = k
	}
	return subst, "", ""
}

// ifaceHeaderName is the interface's bare name on an impl block header,
// whichever of the three spellings was used, and false when the expression
// names no interface at all.
//
// `impl Iface for T`, `impl Iface<A, B> for T` and `impl mod.Iface for T` all
// name ONE interface. Module qualification is
// seen through for the reason the parser's own `implInterfaceTypeArgs` sees
// through it: the qualifier picks the declaring module, not the interface.
func ifaceHeaderName(te ast.TypeExpr) (string, bool) {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name, true
	case *ast.GenericType:
		return t.Name, true
	case *ast.QualifiedType:
		return ifaceHeaderName(t.Member)
	}
	return "", false
}

// ifaceHeaderArgs are the type arguments written on an impl block's interface,
// seeing through module qualification exactly as the parser's
// `implInterfaceTypeArgs` does.
func ifaceHeaderArgs(te ast.TypeExpr) ([]ast.TypeExpr, bool) {
	switch t := te.(type) {
	case *ast.GenericType:
		if len(t.Params) == 0 {
			return nil, false
		}
		return t.Params, true
	case *ast.QualifiedType:
		return ifaceHeaderArgs(t.Member)
	}
	return nil, false
}

// pushIfaceSubst makes the instantiation's type arguments visible to typeOf.
//
// The SAME stack generictype.go, genericmono.go and genericimpl.go push onto.
// One insertion point, consulted from typeOf's `*ast.SimpleType` arm and from
// `project()`'s type-parameter arm, is why a nested spelling needs no arm here.
func (g *gen) pushIfaceSubst(subst map[string]kind) {
	frame := make(map[string]kind, len(subst))
	for n, k := range subst {
		frame[n] = k
	}
	g.genericSubst = append(g.genericSubst, frame)
}

func (g *gen) popIfaceSubst() {
	g.genericSubst = g.genericSubst[:len(g.genericSubst)-1]
}

// ifaceMethodTypeParamIsOpen reports whether this interface method mentions a
// type parameter NOTHING HAS BOUND.
//
// # The distinction, and it is the whole of the rule
//
// A generic interface method can mention two kinds of type parameter and they
// are not the same construct:
//
//	fn top(p: self) -> T where T: Comparable                        // the INTERFACE's T
//	fn prefer<K>(r: self, l: K, r: K) -> Bool where K: Comparable   // the METHOD's K
//
// `T` is bound by the IMPL BLOCK, so inside an instantiation it is a concrete
// type and `where T: Comparable` is a question about THAT type. `K` is bound by
// the CALL SITE, so an impl block cannot supply it and no amount of
// monomorphization reaches it — that is the dictionary's population and it keeps
// refusing.
//
// # One derivation
//
// The method's own type parameters and its `where` subjects are one question,
// asked here once: `fn pick<K>(…) where K: Comparable`, the only open shape
// reachable from source, has both, and both answer `generic interface
// function`. Two separate checks would each mask the other's absence.
//
// # Why discharging a ground constraint is sound
//
// It is `registerInstanceImpls`' argument, verbatim and for the same reason: A
// VIOLATING INSTANTIATION NEVER REACHES THIS BUILDER. The front end checks
// `where T: Comparable` against the solved type argument at the impl and at
// every call, and `type_parameter_dispatch_test.nomi` asserts the exact message
// (`no impl of `Display` for `Plain“) — notably from inside a `compiler.check`
// string, i.e. the violating program is a test's SUBJECT and is never a program
// irbuild is asked to lower.
//
// So a check here would be a SECOND implementation of a front-end rule over an
// empty reachable population.
//
// # Additive by construction
//
// Outside any instantiation `g.genericSubst` is empty, so every subject is
// unbound and every clause refuses. So this can only turn a refusal into a
// lowering.
func (g *gen) ifaceMethodTypeParamIsOpen(im *ast.InterfaceMethod) bool {
	// A method's OWN type parameter, and the FRAME is consulted rather than the
	// count: ifaceMethodMonoCall binds a method's own parameter per call, and
	// under that frame `K` is a ground kind. With no frame open every parameter
	// is unbound and every clause refuses.
	if g.unboundTypeParams(im.TypeParams) {
		return true
	}
	for _, wc := range im.WhereClauses {
		// `where self: X` bounds the implementor, which a default is lowered
		// per; it names no type parameter to bind.
		if wc.Name == "self" {
			continue
		}
		if _, bound := g.genericSubstKind(wc.Name); !bound {
			return true
		}
	}
	return false
}
