package irbuild

// `Debug.inspect(x)` where x's CONCRETE TYPE IS CHOSEN AT RUN TIME: a value
// erased to an interface, or a bare type parameter the checker gave a `Debug`
// bound.
//
// # Why this is the one interface that dispatches on a box it did not erase
//
// `tableCall` refuses a receiver whose box was erased under a DIFFERENT
// interface — `existential of another interface` — and it is right to, for every
// interface but this one. `Display.to_string(v)` on a `v: Tagged` is a program
// the checker rejects unless `Tagged` extends `Display`, so a dispatch site
// reaching one has lost track of which contract it was accepted under.
//
// `Debug` is different by CONSTRUCTION and not by convention. The front end
// synthesizes an `impl Debug for T` for every declared type
// (`analysis.SynthesizeUniversalDebug`), and `analysis/checker.go`'s
// `typeImplementsInterface` answers TRUE for `Debug` at every concrete type and
// every bare type variable. So `Debug` is satisfied by everything, and a
// `rt.Dyn` erased under ANY interface holds a value whose type has a Debug
// impl — the `*rt.TypeID` in the box is all a lookup needs, and which interface
// erased it carries no information the lookup uses.
//
// That is why this file exists rather than a relaxation inside `tableCall`:
// relaxing the receiver check there would relax it for `Display` and
// `Comparable` too, where it is the only thing standing between a dispatch and a
// contract nobody checked.
//
// # The demand, and why it is not eager
//
// With the `Debug` row in `stdIfaceSpecs`, an eager `bindImpl` would produce a
// binding for EVERY declared type, because every declared type has a synthesized
// Debug impl: one `rt.MDebugInspect.Bind` per type even in a program with no
// erased Debug anywhere. That is registration proportional to the PROGRAM rather
// than to its Debug use.
//
// So Debug's bindings are DEMAND-DRIVEN, on `flushInspectors`' model — built
// at flush time for exactly the types some erased dispatch can reach, and not at
// all for a program that never erases one. The demand has two halves and both
// are recorded at a site that already exists, so nothing walks a body:
//
//	the DISPATCH   debugErasedCall — "an erased Debug dispatch happened on a box
//	               of interface I"
//	the BOXING     erase — "concrete kind k was boxed into an existential of I"
//
// The intersection is exact: a box arriving at a Debug dispatch on an `I` was
// created by boxing something into an `I`, so binding every k recorded against
// every demanded I binds precisely the reachable set. Nothing is bound for an
// interface no Debug dispatch names, and nothing is bound for a type never
// boxed.
//
// WHAT THIS DOES NOT COVER, stated rather than discovered: a box created in one
// unit package and Debug-dispatched in another. The two halves are per-gen,
// so the demand would not meet. `debugErasedCall` refuses a MIRRORED interface
// def by name for exactly that reason — a mirror is how a foreign interface
// reaches this gen, so refusing one is refusing the cross-package case at its
// only door.

// debugDeferred reports whether bindImpl must LEAVE this method to
// flushErasedBindings instead of binding it beside the impl.
//
// True for exactly `Debug.inspect` through the shared std def, and the reason is
// the one the file comment measures: `Debug` is universal, so binding at the
// impl registers every declared type in the program whether or not any erased
// dispatch exists. Every other interface — including a USER interface a program
// happens to name `Debug` — binds eagerly as before, because its impls are
// things a programmer wrote and their count is already proportional to use.
func (g *gen) debugDeferred(d *ifaceDef, m *ifaceMethod) bool {
	if d == nil || m == nil || m.name != "inspect" {
		return false
	}
	return isStdDebug(d)
}

// isStdDebug reports whether d is the process-wide shared def for std's `Debug`.
//
// ONE encoding, asked by every arm that needs it. The name is safe here and only
// here: `stdShared` is set in exactly one place, on the defs stdIfaceDefs builds,
// so membership in that set is pointer-anchored and the name only picks a member
// OUT of it. A user's own `pub interface Debug` never has the flag, which is the
// case a name-keyed lookup would get wrong — see stdiface.go's identity note.
func isStdDebug(d *ifaceDef) bool {
	return d != nil && d.stdShared && d.nomi == "Debug"
}

// erasedImplResolves reports whether the builder names the implementation of
// one (interface, method, receiver kind) for an erased receiver.
//
// Two sources, and they are the SAME two a statically-known receiver already
// resolves through:
//
//	a named type   this module's registered impl — the table
//	               `foreignIfaceCall` and `interfaceCall` consult
//	a scalar       std's own impl at that receiver kind — the `byIface` row
//	               `Debug.inspect(42)` reads
func (g *gen) erasedImplResolves(d *ifaceDef, m *ifaceMethod, recv kind) bool {
	if m.shape.recvAt() < 0 || !m.dispatchable() {
		return false
	}
	if !g.hasTID(recv) {
		return false
	}
	if impl := g.implsByIface[d.nomi][recv]; impl != nil && impl.lowerable {
		if it := impl.items[m.name]; it != nil && it.lowerable && len(it.params) == len(m.params) {
			return true
		}
	}
	if !isScalarKind(recv) || g.std == nil {
		// Only a SCALAR has a std impl the builder can name: a named stdlib
		// type's impl is std's own `impl` block.
		return false
	}
	params := make([]kind, len(m.params))
	for i := range m.params {
		if m.shape.selfTyped(i) {
			params[i] = recv
			continue
		}
		params[i] = m.params[i]
	}
	f := stdPick(g.std.byIface[d.nomi+"."+m.name][recv], params)
	if f == nil || f.why != "" || len(f.params) != len(params) || f.result != m.result {
		return false
	}
	for i := range params {
		if f.params[i] != params[i] {
			return false
		}
	}
	return true
}

// bindsStdScalarImpl reports whether flushErasedBindings can bind d's methods at
// the SCALAR kind k out of the stdlib index.
//
// This is what makes `erase` box a scalar into a std interface at all: `erase`
// returns the value UNBOXED when the program lowers no impl, and a stdlib impl
// of a stdlib interface is not an `impl` block in any module, so `bindsImpl`
// answered no and every bound-over-a-scalar generic refused. That refusal is
// `gapNoImplForBound`, whose recorded population is exactly this.
//
// EVERY method, not any: a table with one of two methods bound traps on the
// other, and the checker accepted the program.
func (g *gen) bindsStdScalarImpl(d *ifaceDef, k kind) bool {
	if d == nil || !d.stdShared || !isScalarKind(k) || len(d.order) == 0 {
		return false
	}
	for _, m := range d.order {
		if !m.table {
			return false
		}
		if !g.erasedImplResolves(d, m, k) {
			return false
		}
	}
	return true
}

// isScalarKind is the receiver shape for which the stdlib index names an
// implementation.
//
// Local to this file rather than a method on `kind`, deliberately: "scalar" is
// spelled as a tag list at five sites in this package with five slightly
// different memberships (inspectcall.go's field rule includes tagUnit,
// stdprelude.go's package-neutrality does not include tagNamed), and a shared
// predicate would have to pick one and quietly change four. The membership here
// is the one the stdlib `byIface` index is keyed at — the five kinds
// `stdlib.go`'s scalarKind resolves.
func isScalarKind(k kind) bool {
	switch k.tag {
	case tagUnit, tagInt, tagFloat, tagString, tagBool:
		return k.comp == nil
	}
	return false
}
