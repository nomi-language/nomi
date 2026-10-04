package irbuild

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Anonymous structs: a RECORD whose identity is its field set.
//
// # The representation is a Go anonymous struct, canonicalized by field name
//
// `{x: Int, y: String}` becomes `struct { F_x int64; F_y string }`, which is
// collections.go's tuple representation with the positions replaced by names.
// The argument for it is the same one and it is stronger here: Go's struct type
// identity is STRUCTURAL, and a Nomi anonymous struct's identity is structural
// too, so the two agree with nothing of ours in between. A named type per
// shape would need a name derived injectively from the shape, a collision
// guard, and a declaration made exactly once per shape — three mechanisms
// reconstructing something Go already has.
//
// The one thing Go does NOT already have is ORDER-INSENSITIVITY. A Nomi
// anonymous struct is unordered — `{x: 1, y: 2}` and `{y: 2, x: 1}` are ONE
// type, which analysis.TypesEqual settles by field NAME rather than by position
// — while a Go struct type is ordered, so the two spellings would be two Go
// types. Every kind built here is therefore canonicalized by sorting on the
// NOMI field name, which makes the two spellings one interned *compKind.
//
// # It must NOT unify with a nominal struct of the same shape
//
// Nominal identity is `(declaring file, name)` and never the shape (types.go's
// header). A `struct Point { x: Int, y: Int }` lowers
// to a Go DEFINED type `NomiT_Point`, and `{x: Int, y: Int}` lowers to a bare
// `struct { F_x int64; F_y int64 }`. The builder's kinds keep the two apart:
// `tagNamed` carries a *typeDef and `tagAnonStruct` carries a *compKind.
// testdata/anon_vs_nominal.nomi is the differential pin.
//
// # The intern key, and why the names are in it
//
// composite.go keys a structural type on its Nomi spelling plus the component
// kinds. A record adds the field names, in canonical order, as a third part of
// the key, so the identity does not depend on how the spelling is produced.
//
// # Debug and Display render SORTED, which the canonical form gives for free
//
// A record renders through rt.InspectStruct, which sorts the `name: value`
// pairs by field name and omits the type name when there is none, so
// `{outer: …, count: 3}` inspects as `{count: 3, outer: …}`
// (tests/07-structs-and-enums/anonymous_structs). The inspector passes
// "" as the type name, so the order comes from rt's sort rather than from the
// builder's field order. See inspect.go.

// anonStructKind is the kind of the record with these fields.
//
// names and parts are parallel and in SOURCE order; the canonical order is
// established here so no caller has to know about it. kindInvalid when a field
// type has no representation (the caller's own walk has already counted that),
// when two names reach one Go identifier, or when there are no fields at all —
// a record with no fields is not a shape this builder has a use for, and
// `struct {}` would silently equal Unit's neighbours.
func (g *gen) anonStructKind(names []string, parts []kind) kind {
	fields, ok := canonicalAnonFields(names, parts)
	if !ok {
		return kindInvalid
	}
	var nomi strings.Builder
	nomi.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			nomi.WriteString(", ")
		}
		nomi.WriteString(f.nomi + ": " + f.k.nomi())
	}
	nomi.WriteByte('}')
	sortedNames := make([]string, len(fields))
	sortedParts := make([]kind, len(fields))
	for i, f := range fields {
		sortedNames[i], sortedParts[i] = f.nomi, f.k
	}
	return kind{
		tag:  tagAnonStruct,
		comp: g.internNamed(nomi.String(), sortedNames, sortedParts),
	}
}

// anonField is one record field in canonical order.
type anonField struct {
	nomi string
	k    kind
}

// canonicalAnonFields sorts the fields by Nomi name and reports whether the
// record is representable at all.
func canonicalAnonFields(names []string, parts []kind) ([]anonField, bool) {
	if len(names) == 0 || len(names) != len(parts) {
		return nil, false
	}
	fields := make([]anonField, len(names))
	for i := range names {
		// A kind CONSTRUCTOR: the field's own decline is counted where it was walked.
		// kindInvalid: propagates — the record has no representation without the field.
		if parts[i] == kindInvalid {
			return nil, false
		}
		fields[i] = anonField{nomi: names[i], k: parts[i]}
	}
	sort.Slice(fields, func(a, b int) bool { return fields[a].nomi < fields[b].nomi })
	return fields, true
}

// anonFieldKind is the kind of one field of a record kind, and reports whether
// the record has a field of that name.
func anonFieldKind(k kind, name string) (kind, bool) {
	if k.tag != tagAnonStruct || k.comp == nil {
		return kindInvalid, false
	}
	for i, n := range k.comp.names {
		if n == name {
			return k.comp.parts[i], true
		}
	}
	return kindInvalid, false
}

// --- the type position -------------------------------------------------------

// anonStructTypeOf is the kind an `{name: T, …}` annotation names.
func (g *gen) anonStructTypeOf(t *ast.AnonStructType) kind {
	names := make([]string, len(t.Fields))
	parts := make([]kind, len(t.Fields))
	for i, f := range t.Fields {
		names[i] = f.Name
		parts[i] = g.typeOf(f.TypeAnnotation)
	}
	return g.anonStructKind(names, parts)
}

// --- the literal position ----------------------------------------------------

// --- reading a field ---------------------------------------------------------

// --- matching ----------------------------------------------------------------
