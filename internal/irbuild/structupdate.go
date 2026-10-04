package irbuild

// `Struct.update(original, {field: value})` — the universal struct patch.
//
// # Why this is a CALL-SITE lowering and not an anchor
//
// Every other member of `stdlib function outside the scalar subset` is refused
// because a type in its signature has no runtime form, and every other fix is
// therefore to give that type one. `Struct.update` is the exception, and the
// difference is not a matter of degree:
//
//	host fn update(original: self, updates: Partial<self>): self
//
// std/structs.nomi's own comment says what `Partial<T>` is — "a compiler-known
// built-in type operator ... It has no import and no declaration anywhere in
// stdlib (unlike `List`/`Map`, which are real `host type`s): it has no
// inhabitants". A type with no inhabitants cannot be given a representation,
// because there is nothing to represent. No `rt.Partial`, no spec row, and no
// widening of the scalar subset reaches this signature.
//
// What CAN be lowered is the call, because at a call site the "partial" is a
// literal whose field set is known: `{name: "Bob"}` names exactly one field.
// So the type operator never needs a runtime form; the patch is resolved where
// the shape is written down. That is also why this arm DECLINES rather than
// refuses for anything it cannot resolve — see structUpdateCall.
//
// # The merge rule, which is std's and not this file's
//
// std/structs.nomi states it: "fields present on both take the value from
// `updates`", and it is a DEEP partial — "a nested struct field may itself be
// patched recursively (an anonymous struct merges; a concrete struct replaces
// the field wholesale)". Both halves are load-bearing and the corpus
// distinguishes them in adjacent lines:
//
//	Struct.update(person, {address: {city: "NYC"}})            // merges: zip survives
//	Struct.update(person, {address: Address{city:…, zip:…}})   // replaces
//
// So the recursion is driven by the SYNTAX of each update value: an anonymous
// struct literal recurses, anything else is an assignment. Reading the value's
// KIND instead would be wrong in exactly the case above — both values have kind
// `Address` after lowering, and the two lines must not do the same thing.
//
// # Why the emitted form is a copy-and-assign
//
// A Nomi struct lowers to a Go struct VALUE, so `o := original; o.F = v; return
// o` is the patch, with Go's own copy doing the "non-overlapping fields are
// preserved" half for free. It is written as an immediately-applied func
// literal rather than as statements because a call is an EXPRESSION here and
// may appear anywhere one may — including inside another update's field list,
// which is what the nested form emits.
//
// The original is evaluated exactly ONCE, as the argument to that literal.
// Building `T{F: v, G: original.G, …}` instead would repeat it per surviving
// field, which for `Struct.update(expensive(), {x: 1})` is a call per field and
// a different program.

// stdStructUpdateIsStds reports whether `Struct.update` resolves to
// std/structs' declaration in the stdlib index.
//
// Asked of the INDEX rather than of module scope because the index is what
// records where a declaration came from, and `structs.Struct.update` is filed
// under the interface name in the receiver slot — the key
// `lookupImplBlockExtern` builds for a `host fn` declared inside an
// `interface`. See stdlib.go's collectStdCandidates.
func (g *gen) stdStructUpdateIsStds() bool {
	if g.std == nil {
		return false
	}
	for _, f := range g.std.byType["Struct.update"] {
		if f.module == "structs" && f.recv == "Struct" && f.decl == nil {
			return true
		}
	}
	return false
}

// updateField resolves one field name against the receiver's kind, for both
// shapes `Partial<self>` admits: a NAMED struct and an anonymous record.
//
// Two shapes because `Struct` is the interface EVERY struct satisfies, "a named
// `struct` type or an anonymous `{...}`" in std's own words, so an arm that
// served only named ones would lower half the interface and silently decline
// the other half under a key that says nothing about records.
func (g *gen) updateField(k kind, name string) (kind, bool) {
	switch k.tag {
	case tagNamed:
		d := k.def
		if d == nil || d.isEnum || d.isDistinct || !d.lowerable {
			return kindInvalid, false
		}
		for i := range d.fields {
			if d.fields[i].nomi == name {
				return d.fields[i].k, true
			}
		}
	case tagAnonStruct:
		if fk, found := anonFieldKind(k, name); found {
			return fk, true
		}
	}
	return kindInvalid, false
}
