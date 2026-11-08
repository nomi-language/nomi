package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/nomi-language/nomi/internal/ast"
)

// The `Iter` SOURCES beyond List, and the functions that carry per-run state.
//
// iter.go owns the anchor, the kind, the call router and the stateless
// adapters. The seam between the two files is the one in std/iter.nomi
// itself: seq.go's six adapters are stateless one-call wrappers, and
// everything here either introduces a SOURCE or carries PER-RUN STATE. See
// rt/seqsrc.go for the runtime half.
//
// Two refusal keys meet here and are distinct: `unlowered Iter function` names
// an `Iter.X` with no lowering, and `Iter over an unlowered source` names a
// source whose `each_while` has no Go implementation.
// `13-iterators-and-pipes/string_iterators_test.nomi` exercises both, and
// neither implies the other.
//
// # A tuple is an anonymous Go struct, so rt cannot name it
//
// Three functions here produce a tuple — `Map`'s pairs, `with_index`'s
// `(Int, T)`, `partition`'s `(List<T>, List<T>)` — and a Nomi tuple lowers to a
// per-package anonymous struct that rt has no way to spell. So each rt entry
// point takes a CONSTRUCTOR closure and the call site emits it, which is the
// shape maps.go already uses in the other direction for `rt.MapFromList`'s two
// projections. One closure per pipeline BUILD, nothing per element.
//
// # `known_count` is answered from the static kind
//
// `Iter.count` already resolves the `known_count` protocol at compile time from
// the source's kind rather than at run time through dispatch. `known_count`
// itself does the same, and the two must agree — a List answers `Some(n)`
// from its cached length in both, a Map from its stored size, and a String and
// a lazy `Seq` decline. `String` declining is the load-bearing one: a grapheme
// cluster count is inherently O(n), so `known_count` on text is `None` and the
// O(1) spelling is `String.length`. Making it countable would be a wrong answer
// (bytes are not clusters) or a silent O(n) behind a name that promises O(1).

// --- sources -----------------------------------------------------------------

// --- known_count ---------------------------------------------------------------

// --- the functions -------------------------------------------------------------

// --- the two terminals that BUILD a Map --------------------------------------
//
// `to_map` and `group_by` need a Map BUILT rather than walked, as `to_set`
// needs a Set: rt has a persistent Map, and these are the terminals that fill
// one. Neither needs a source walked differently.
//
// Both resolve the key's hash and equality STATICALLY from the key kind, through
// the same mapOps every other Map call site uses — so a key that groups together
// here is a key that compares equal everywhere else, and a key type the Map
// representation cannot carry refuses under `map key type` rather than under a
// name of this file's own.

// isStdHostPrim reports whether k is the stdHostSpecs row identified by the
// analyzer singleton prim.
//
// A small reader of one table, here rather than in stdhost.go because the
// only callers are this file's and stdhost.go belongs to the stdlib surface. The
// pointer comparison is the identity; see isDynamicKind for the argument.
func isStdHostPrim(k kind, prim *analysis.PrimitiveType) bool {
	for i := range stdHostSpecs {
		if stdHostSpecs[i].prim != prim {
			continue
		}
		// kindInvalid: sentinel — a kind with no def is not this named type.
		return k.def != nil && k.def == stdHostDefs()[i]
	}
	return false
}

// --- the three with a `where` bound --------------------------------------------
//
// `sort`, `sort_by` and `chunk_by` are `sort_with`/`chunk_by_each` plus a
// comparator or an equality built from the ELEMENT's (or the projected KEY's)
// type. Their declarations carry `where T: Comparable` / `where K: Equatable`,
// and that bound is discharged at the call site by the concrete type argument
// — the builder knows the element kind statically, so it can build the
// comparator rather than look it up at run time.
//
// They do not wait on the type-parameter dictionary; the measurement is in
// iterMaxArity's header. What the dictionary really blocks
// is one level down — `Comparable.compare` at a type parameter INSIDE a
// generic impl body, which is `impl Comparable for List<T>` and is why a
// nested list's element comparator is restricted to Int and Float. So these
// refuse by the ELEMENT KIND, naming the thing that is missing, rather than by
// the function.

// sortOrders reports whether element kind k has `Comparable.compare` resolved
// statically at a concrete type.
//
// compareResolves is the one resolver, shared with `<` and `List.compare`, so a
// sort orders elements exactly the way the comparison operators do. Its own
// boundary is inherited rather than re-stated here: a nested list's element
// goes through elemOrders, which serves Int and Float only because those are
// the two whose ordering answers unconditionally. Everything else refuses at
// build time rather than faulting at run time.
func (g *gen) sortOrders(k kind, at ast.Node) bool {
	return g.compareResolves(at, k, stdEnumKind(stdEnumOrdering))
}
