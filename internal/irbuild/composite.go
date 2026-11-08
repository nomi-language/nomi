package irbuild

// Structural types: identity for the types that have no declaration.
//
// A named type's identity is the pointer to the declaration that produced it
// (see types.go's package comment); spelling identity as a string gives silent
// wrong answers when two declarations share a name. A STRUCTURAL type —
// `(Int) -> Int`, `List<Int>`, `(Int, String)` — has no declaration to point
// at, and one `kind` tag covers unboundedly many distinct Go types. Comparing
// such kinds by tag alone would make `(Int) -> Int` equal to `(String) -> Bool`.
//
// The fix is to give a structural type a declaration-shaped identity by
// INTERNING it: one `*compKind` per distinct type, so pointer equality is
// structural equality and `kind`'s plain `==` keeps working unchanged for every
// kind in the language.
//
// The table is per module HERE and process-wide in sharedcomp.go, and which one
// answers is decided by the components rather than by the caller: a composite
// whose every component is package-neutral renders identically in every
// unit package, so it takes process-wide identity and can therefore cross
// the stdlib boundary. Everything else — anything naming a type one unit
// package declares — stays here. sharedcomp.go carries that argument; this file
// carries the KEY's.
//
// # The key is the Nomi spelling PLUS the component kinds
//
// Entries are grouped by the Nomi spelling and separated within a group by
// `parts`, the component identity list. A `kind` is a tag plus a declaration
// pointer, so comparing parts compares identities and never spellings. The
// spelling alone is not enough: two files may each declare a `Point`, and
// `List<Point>` over each is spelled alike and is two types.
//
// Two payloads share an enum slot only when their kinds are equal, so the key
// can only ever produce an extra slot, never a truncation of one into another.
//
// # What this table does NOT answer
//
// An interned pointer answers exactly one question: **is this the same
// structural type?** It does not answer "is this the same NOMI type?", and a
// caller that reads it as if it did gets an answer that is sound for storage
// and wrong for everything else — the worst combination, because nothing
// fails.
//
// Two shapes of that trap, one in each direction:
//
//   - Many Nomi types, one storage class. Every interface existential is held
//     the same way, so `Speaker` and `Greeter` must not collapse to one
//     pointer, or the interface identity dispatch selects a method table with
//     would be gone. An interface-typed kind is never itself an entry here; it
//     carries a declaration pointer instead (see impl.go). It can be a
//     COMPONENT of one, and then `parts` keeps the two apart.
//   - One Nomi type, many Go types — the case this table exists for, and the
//     reason the tag alone cannot be the identity.
//
// So: use intern() when the question is storage. Use a declaration pointer
// when the question is which Nomi type this is.

// compKind is one interned structural type.
type compKind struct {
	// nomi is the Nomi spelling. With the components it is the identity.
	nomi string
	// parts are the component kinds in the order the spelling names them: a
	// function type's parameters followed by its result, a list's element,
	// an anonymous struct's fields in CANONICAL (sorted-by-name) order.
	// Carried so a walk over a composite's components (cycle detection, a
	// size probe) does not have to re-parse the spelling.
	parts []kind
	// names are an anonymous struct's field names, parallel to parts and in
	// the same canonical order; nil for every other structural type, whose
	// components are addressed positionally.
	//
	// Part of the KEY as well as of the payload.
	names []string
}

// intern returns the one *compKind for this structural type, creating it on
// first sight. The router is sharedcomp.go's internComp: it decides which
// table answers, and both key the same way.
func (g *gen) intern(nomi string, parts ...kind) *compKind {
	return internComp(g, nomi, parts...)
}

// internNamed is intern for a structural type whose components are addressed
// by NAME: an anonymous struct. See anonstruct.go.
func (g *gen) internNamed(nomi string, names []string, parts []kind) *compKind {
	return internCompNamed(g, nomi, names, parts)
}

// internLocal is this MODULE's table, for a composite naming a type only this
// unit declares. Entries are grouped by Nomi spelling and separated by their
// components and field names, which are the identity: two same-named types
// from two files are two components.
func (g *gen) internLocal(nomi string, names []string, parts []kind) *compKind {
	if g.comps == nil {
		g.comps = map[string][]*compKind{}
	}
	for _, c := range g.comps[nomi] {
		if samePartsSlice(c.parts, parts) && sameNameSlice(c.names, names) {
			return c
		}
	}
	c := &compKind{nomi: nomi, parts: parts, names: names}
	g.comps[nomi] = append(g.comps[nomi], c)
	return c
}
