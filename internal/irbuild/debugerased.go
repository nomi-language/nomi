package irbuild

// `Debug.inspect(x)` where x's CONCRETE TYPE IS CHOSEN AT RUN TIME: a value
// erased to an interface, or a bare type parameter the checker gave a `Debug`
// bound.
//
// # Debug is satisfied by everything
//
// `Display.to_string(v)` on a `v: Tagged` is a program the checker rejects
// unless `Tagged` extends `Display`. `Debug` is different by construction. The
// front end synthesizes an `impl Debug for T` for every declared type
// (`analysis.SynthesizeUniversalDebug`), and the checker answers true for
// `Debug` at every concrete type and every bare type variable. So a value
// erased under any interface has a type with a Debug impl, and which interface
// erased it carries no information the lookup uses: the VM picks the impl by
// the value's run-time type name.

// debugDeferred reports whether bindImpl leaves this method out of its check
// that the impl's receiver has a runtime identity.
//
// True for exactly `Debug.inspect` through the shared std def. `Debug` is
// universal: every declared type has a synthesized impl of it, and bindImpl
// does not hold those impls to the check. Every other interface, including a
// user interface a program happens to name `Debug`, is checked.
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

// stdDebugDef is the process-wide shared def for std's `Debug`, reached
// without an anchor in the asking file (impl.go's synthesized-Debug arm).
func (g *gen) stdDebugDef() *ifaceDef {
	for _, d := range stdIfaceDefs() {
		if isStdDebug(d) {
			return d
		}
	}
	return nil
}

// erasedImplResolves reports whether the builder names the implementation of
// one (interface, method, receiver kind) for an erased receiver.
//
// Two sources, and they are the SAME two a statically-known receiver already
// resolves through:
//
//	a named type   this module's registered impl, the `implsByIface` table
//	               a call on a statically-known receiver consults
//	a scalar       std's own impl at that receiver kind — the `byIface` row
//	               `Debug.inspect(42)` reads
func (g *gen) erasedImplResolves(d *ifaceDef, m *ifaceMethod, recv kind) bool {
	if m.shape.recvAt() < 0 || !m.dispatchable() {
		return false
	}
	if !g.hasTID(recv) {
		return false
	}
	if impl := g.implsByIface[g.implsKey(d)][recv]; impl != nil && impl.lowerable {
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

// bindsStdScalarImpl reports whether the stdlib index names an implementation
// of every method of d at the scalar kind k.
//
// bindsImpl asks it first: a stdlib impl of a stdlib interface is not an
// `impl` block in any module, so the impl indexes hold none, and without this
// a scalar boxed into a std interface would be answered as having no impl.
//
// Every method, not any: a value with one of two methods resolved would trap
// on the other, and the checker accepted the program.
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
// different memberships (kind.packageNeutral's includes tagNamed), and a shared
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
