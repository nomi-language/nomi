package irbuild

// `Debug.inspect(x)` where x's CONCRETE TYPE IS CHOSEN AT RUN TIME: a value
// erased to an interface, or a bare type parameter the checker gave a `Debug`
// bound.
//
// # Debug is satisfied by everything
//
// `Display.to_string(v)` on a `v: Tagged` is a program the checker rejects
// unless `Tagged` extends `Display`. `Debug` is different by construction:
// every type implements it, and the checker answers true for `Debug` at
// every concrete type and every bare type variable. A type with a written or
// derived Debug, an opaque type and a host type have a block of it (the last
// two a name-only one, `analysis.SynthesizeUniversalDebug`); any other
// renders by its shape (debugByShape). So a value erased under any interface
// renders, and which interface erased it carries no information the
// rendering uses: the VM picks a body by the value's run-time type name, or
// renders the value by its shape where none is named.

// debugDeferred reports whether bindImpl leaves this method out of its check
// that the impl's receiver has a runtime identity.
//
// True for exactly `Debug.inspect` through the shared std def. `Debug` is
// universal, and bindImpl does not hold its impls to the check. Every other
// interface, including a user interface a program happens to name `Debug`,
// is checked.
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
	if impl := g.implAt(g.implsKey(d), recv); impl != nil && impl.lowerable {
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
	f := stdPick(g.std.stdIfaceImpls(g.stdIfaceType(d).Func(m.name), recv), params)
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

// bindsStdSharedImpl reports whether std implements d, one of the shared
// std defs (stdIfaceDefs: Display, Debug, Comparable), at k, so that a value
// of k held as d answers every call on it.
//
// bindsImpl asks it first: a stdlib impl of a stdlib interface is not an
// `impl` block in any module, so the impl indexes hold none, and without this
// a std value held as a std interface would be answered as having no impl.
//
// A function an erased receiver can dispatch needs the implementation the
// stdlib index names at k, which only a scalar has (erasedImplResolves).
// Every one, not any: a value with one of two methods resolved would trap
// on the other, and the checker accepted the program. A function no erased
// receiver can dispatch (`Comparable.compare`'s second `self`,
// gapMultiSelf) is refused at its call, as stdProgBinds refuses it for a
// declared type, so it asks only that std implements it at k: a std type
// held as Comparable (`x: Comparable = 1.5d`) asks nothing of the value.
func (g *gen) bindsStdSharedImpl(d *ifaceDef, k kind) bool {
	if d == nil || !d.stdShared || len(d.order) == 0 || g.std == nil {
		return false
	}
	for _, m := range d.order {
		if !m.table {
			return false
		}
		if !m.dispatchable() {
			if len(g.std.stdIfaceImpls(g.stdIfaceType(d).Func(m.name), k)) == 0 {
				return false
			}
			continue
		}
		if !isScalarKind(k) || !g.erasedImplResolves(d, m, k) {
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
