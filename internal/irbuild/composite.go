package irbuild

// Structural types: identity for the types that have no declaration.
//
// A named type's identity is the pointer to the declaration that produced it
// (see types.go's package comment); spelling identity as a string gives silent
// wrong answers when two declarations share a name. A structural type,
// `(Int) -> Int`, `List<Int>` or `(Int, String)`, has no declaration to point
// at, and one `kind` tag covers unboundedly many distinct types. Comparing
// such kinds by tag alone would make `(Int) -> Int` equal to `(String) -> Bool`.
//
// A structural type gets a declaration-shaped identity by interning: one
// `*compKind` per distinct type, so pointer equality is structural equality
// and `kind`'s plain `==` works for every kind in the language.
//
// The table is per module here and process-wide in sharedcomp.go, and the
// components decide which one answers, not the caller. A composite whose
// every component is package-neutral (a scalar, a process-wide def, an
// anchored interface) is the same type in every module, so it takes
// process-wide identity, and a std signature's kind and a call site's kind
// for it are one pointer. Anything naming a type one module declares stays
// here. sharedcomp.go says why the shared table exists; this file describes
// the key.
//
// # The key is the Nomi spelling plus the component kinds
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
// # What this table does not answer
//
// An interned pointer answers one question: is this the same structural
// type? It says nothing about a declared type. An interface-typed kind is
// never an entry here: `Speaker` and `Greeter` each carry a declaration
// pointer (see impl.go), so the two stay two kinds. An interface kind can be
// a component of an entry, and then `parts` keeps the two entries apart.
//
// Use intern() for a structural type and a declaration pointer for a
// declared one.

// compKind is one interned structural type.
type compKind struct {
	// nomi is the Nomi spelling. With the components it is the identity.
	nomi string
	// parts are the component kinds in the order the spelling names them: a
	// function type's parameters followed by its result, a list's element,
	// an anonymous struct's fields in canonical (sorted-by-name) order.
	// Carried so a walk over a composite's components (appendMentionedDefs,
	// appendInlineDefs) does not have to re-parse the spelling.
	parts []kind
	// names are an anonymous struct's field names, parallel to parts and in
	// the same canonical order; nil for every other structural type, whose
	// components are addressed positionally.
	//
	// Part of the key as well as of the payload.
	names []string
}

// intern returns the one *compKind for this structural type, creating it on
// first sight. The router is sharedcomp.go's internComp: it decides which
// table answers, and both key the same way.
func (g *gen) intern(nomi string, parts ...kind) *compKind {
	return internComp(g, nomi, parts...)
}

// internNamed is intern for a structural type whose components are addressed
// by name: an anonymous struct. See anonstruct.go.
func (g *gen) internNamed(nomi string, names []string, parts []kind) *compKind {
	return internCompNamed(g, nomi, names, parts)
}

// internLocal is this module's table, for a composite naming a type only this
// module declares. Entries are grouped by Nomi spelling and separated by their
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
