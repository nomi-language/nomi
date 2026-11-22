package rt

// A Nomi type's runtime identity, as a pointer.
//
// A type's identity is the address of its TypeID. The VM interns one per
// qualified type name (internal/vm/context.go) and keys on the address: a
// `Type<T>` witness carries it (typewitness.go) and a context's value store
// is keyed by it (context.go). Nothing compares a name, so two same-named
// types in two modules are two TypeIDs and cannot be confused however their
// names are spelled, shortened or cut.
//
// Two Go hazards sit under that, and TestTypeIDsAreDistinctAddresses pins
// both:
//
//  1. Two package-level variables of a zero-sized type may legally share an
//     address. TypeID carries a field, so it is never zero-sized.
//  2. Two variables with byte-identical contents are candidates for merging.
//     A TypeID carries the module-qualified name (`shapes.Point`,
//     `geometry.Point`), which differs for any two distinct types and is the
//     better diagnostic anyway.

// TypeID is one Nomi type's runtime identity. Its address is the identity; the
// value carries nothing a lookup reads.
//
// Nomi is the type's module-qualified Nomi name and is diagnostic only. Nothing
// in this file compares it, and nothing may start: once a name decides a
// lookup, the collapse described above can happen again. See the two hazards
// in the file comment for why the field exists at all and why it must be
// qualified.
type TypeID struct {
	Nomi string
}

// The identities of the types the compiler represents directly rather than
// declaring. They live here, once, because `impl Numberish for Int` is legal
// under the orphan rule in any module and every such impl must key on the same
// Int — one canonical variable is how that is guaranteed rather than hoped for.
var (
	TIDInt    = TypeID{Nomi: "Int"}
	TIDFloat  = TypeID{Nomi: "Float"}
	TIDString = TypeID{Nomi: "String"}
	TIDBool   = TypeID{Nomi: "Bool"}
)
