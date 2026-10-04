package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A GENERIC INTERFACE, monomorphized per impl block.
//
// # The rule
//
// THE DICTIONARY LOWERS WHAT IT CAN AND MONOMORPHIZATION TAKES ITS RESIDUE.
// `interface Announcer<T>` has two use positions and they fall on opposite
// sides of that line:
//
//   - ERASED. `fn f(a: Announcer)` would need one existential kind and one
//     dispatch table for every instantiation at once, because `existential(d)`
//     takes its identity from the `*ifaceDef` POINTER. That is the dictionary's
//     shape — which interface plus which type argument — and it is NOT built
//     here. It stays refused, and structurally so rather than by a check: the
//     TEMPLATE def keeps `lowerable == false`, an instance is never registered
//     in `g.ifaces`, and `typeOf`'s interface arm reads only `g.ifaces`. So no
//     annotation in any file can NAME an instance.
//   - IMPLEMENTED. `impl Announcer for Box` supplies GROUND type arguments —
//     read off the header when they are written, solved from the item
//     signatures when they are not — and every mention of `T` inside the
//     interface then has a concrete kind. That is monomorphization, and it is
//     what this file does.
//
// # WHY THIS IS A REPRESENTATION AND NOT A WIDENING
//
// `inferred.go`'s `project()` is subtractive with no default arm, and the
// failure mode to avoid is widening `kind` so a projection succeeds. Nothing
// here widens: NO new `kind` tag, NO new field on `kind`, and
// no arm added to `zeroSized`, `nomi`, `packageNeutral`, `coerce` or the
// equality switches. An interface instance is an ORDINARY `*ifaceDef` built by
// `resolveIface` — the same function, over the same declaration node — with a
// substitution frame pushed. The whole of the new identity is that there is one
// def per impl block instead of one per declaration, which is `generictype.go`'s
// move at the interface level.
//
// It is `g.genericSubst` that carries the frame, and that sharing is the point
// rather than a convenience: `typeOf`'s `*ast.SimpleType` arm consults it, so
// `T`, `List<T>`, `Maybe<T>` and `(T) -> Bool` are covered at every depth by an
// insertion point that already exists. A fourth substitution stack would be a
// fourth answer to a question three files already share one answer to.
//
// # NO TABLE FOR AN INSTANCE, and that is load-bearing
//
// `resolveIface` mints `m.table` for every method whose signature is
// representable. Two instances of one template would mint two tables from one
// declaration, and neither would ever be DECLARED — `ifaceDecl` walks
// `g.ifaceOrder`, which holds the template.
//
// So `instOf` suppresses table minting, and the erased route then declines for
// want of a table rather than for want of a check. That decline is unreachable,
// for the structural reason above.
//
// # What stays refused
//
//   - An interface-level `where` clause with no type parameters. It is a
//     different construct — a constraint on `self` — and no arm here reads it.
//   - A template whose type parameters cannot ALL be solved from the impl
//     block. Reported as `generic interface` at the impl, naming the parameter
//     that stayed open, so the reader is sent to the position that would have
//     to change.
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

// ifaceInstKey is one instantiation's identity: the impl BLOCK and the RECEIVER
// it was registered at.
//
// Both halves are load-bearing. The block distinguishes two impls of one
// template; the receiver distinguishes two INSTANTIATIONS of one block, which a
// generic receiver produces and which the block alone could not see. See
// ifaceInstanceFor.
type ifaceInstKey struct {
	decl *ast.ImplBlock
	recv kind
}

// ifaceTemplate is the *ast.InterfaceDef of a generic interface declared in this
// module, or nil.
//
// Read off `g.ifaces` rather than kept in a second map: the template def is
// already there, already carries the declaration, and already carries the
// refusal every unreached mention reports. A parallel registry would be a
// second place for "which interfaces are generic" to be answered.
func (g *gen) ifaceTemplate(name string) *ast.InterfaceDef {
	d := g.ifaces[name]
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
	return d.decl
}

// ifaceInstanceFor is the instance an impl block of a generic interface binds
// into, or a refusal naming what stayed open.
//
// INTERNED PER (IMPL BLOCK, RECEIVER KIND). Coherence gives one impl per
// (interface, receiver), but that does not make the block alone an identity
// for a GENERIC receiver: `impl Chooser for Pair<E>` is ONE block that
// `registerInstanceImpls` registers once per INSTANTIATION, so the same
// `*ast.ImplBlock` arrives at `Pair<Int>` and `Pair<Priority>`. A block-keyed
// intern would hand the second the FIRST's def, whose `T` is already bound to
// `Int` (`11-interfaces-and-impls/type_argument_inference`).
//
// Two blocks at one receiver cannot exist (that IS the coherence rule),
// and with no table and no erasable kind an instance's POINTER is still never
// compared against another instance's. So the key is exactly the identity, with
// a comparable shape rather than a tuple of kinds spelled as a string.
func (g *gen) ifaceInstanceFor(tpl *ast.InterfaceDef, ib *ast.ImplBlock, recv kind) (*ifaceInst, string, string) {
	key := ifaceInstKey{decl: ib, recv: recv}
	if inst := g.ifaceInsts[key]; inst != nil {
		return inst, "", ""
	}
	subst, why, detail := g.ifaceInstArgs(tpl, ib)
	if why != "" {
		return nil, why, detail
	}
	d := &ifaceDef{
		nomi:      tpl.Name,
		decl:      tpl,
		methods:   map[string]*ifaceMethod{},
		fields:    map[string]*ifaceField{},
		lowerable: true,
		instOf:    tpl,
		unit:      -1,
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
	return inst, "", ""
}

// ifaceInstArgs solves the template's type parameters against the impl block.
//
// TWO SOURCES, and the explicit one wins because it is what the programmer
// wrote:
//
//	impl Add<Score, Score> for Score      // header, positional
//	impl Announcer for Box { … }          // solved from the item signatures
//
// The solved form is a parallel walk of the interface method's declared type
// expression against the impl item's, binding a template parameter NAME to the
// kind the impl side resolves to. It is deliberately not an inference engine: it
// binds where the two spellings agree structurally and binds nothing where they
// do not, and any parameter left open is a refusal rather than a guess. The
// front end has already checked conformance, so a disagreement here means this
// walk cannot see the correspondence — not that the program is wrong.
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

	byName := map[string]*ast.FuncDef{}
	for _, item := range ib.Items {
		if fd, isFn := implItemFunc(item); isFn {
			byName[fd.Name] = fd
		}
	}
	open := map[string]bool{}
	for _, n := range names {
		open[n] = true
	}
	for i := range tpl.Methods {
		im := &tpl.Methods[i]
		fd := byName[im.Name]
		if fd == nil {
			continue
		}
		for j := range im.Params {
			if j >= len(fd.Params) {
				break
			}
			g.unifyIfaceParam(im.Params[j].TypeAnnotation, fd.Params[j].TypeAnnotation, open, subst)
		}
		g.unifyIfaceParam(im.ReturnTypeExpr, fd.ReturnTypeExpr, open, subst)
	}
	for _, n := range names {
		if _, bound := subst[n]; !bound {
			// The parameter that stayed open, named. A bare `generic interface`
			// here would send the reader to the DECLARATION, which is fine, and
			// would not say which of `<K, V>` the impl failed to pin.
			return nil, "generic interface", tpl.Name + " type parameter " + n
		}
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

// unifyIfaceParam walks the interface's declared type expression alongside the
// impl's, binding an OPEN template parameter to the kind the impl side denotes.
//
// Structural and shallow on purpose. Where the two spellings correspond it
// binds; where they do not it binds nothing and the caller refuses. That
// asymmetry is the fail-safe direction: an unbound parameter is a named
// refusal, whereas a wrongly bound one is a silent wrong answer.
func (g *gen) unifyIfaceParam(tmpl, impl ast.TypeExpr, open map[string]bool, subst map[string]kind) {
	if tmpl == nil || impl == nil {
		return
	}
	switch t := tmpl.(type) {
	case *ast.SimpleType:
		if !open[t.Name] {
			return
		}
		if _, done := subst[t.Name]; done {
			return
		}
		// An unresolvable impl-side annotation leaves the parameter OPEN, and
		// ifaceInstArgs then refuses `generic interface` naming that parameter.
		k := g.typeOf(impl)
		// kindInvalid: lookup — a miss leaves the parameter unbound; ifaceInstArgs names it.
		if k == kindInvalid {
			return
		}
		subst[t.Name] = k
	case *ast.GenericType:
		gi, same := impl.(*ast.GenericType)
		if !same || gi.Name != t.Name || len(gi.Params) != len(t.Params) {
			return
		}
		for i := range t.Params {
			g.unifyIfaceParam(t.Params[i], gi.Params[i], open, subst)
		}
	case *ast.FuncType:
		fi, same := impl.(*ast.FuncType)
		if !same || len(fi.Params) != len(t.Params) {
			return
		}
		for i := range t.Params {
			g.unifyIfaceParam(t.Params[i], fi.Params[i], open, subst)
		}
		g.unifyIfaceParam(t.Return, fi.Return, open, subst)
	case *ast.QualifiedType:
		qi, same := impl.(*ast.QualifiedType)
		if !same || qi.Module != t.Module {
			return
		}
		g.unifyIfaceParam(t.Member, qi.Member, open, subst)
	}
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
//	fn top(p: self): T where T: Comparable                        // the INTERFACE's T
//	fn prefer<K>(r: self, l: K, r: K): Bool where K: Comparable   // the METHOD's K
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
		if _, bound := g.genericSubstKind(wc.Name); !bound {
			return true
		}
	}
	return false
}
