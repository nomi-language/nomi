package rt

// `Ordering` (std/comparable.nomi), as a Go type.
//
// # Why it is here
//
// The same reason Maybe and Result are (see prelude.go): a named type's
// identity in internal/irbuild is the *typeDef its declaration produced, and a
// std type needs one Go type every module's code can name.
//
// Ordering has a second, sharper reason. It is what `Comparable.compare`
// RETURNS, and the compare impls live in std — `Int.compare` in std/int.nomi,
// `String.compare` in std/strings.nomi, `Float.compare` in std/float.nomi —
// while the callers live in the user's own modules. The value crosses that
// boundary on every `<`, so there is no arrangement in which one module owns
// the type.
//
// # The layout is the shared enum representation, not a new one
//
// A tagged struct with
// tag 0 RESERVED INVALID, so `var o Ordering` is detectably never-constructed
// rather than silently being the first variant. Three bare variants, so there
// are no payload fields at all — an Ordering is one byte.
//
// # The rank mapping has ONE implementation
//
// `<`, `>`, `<=`, `>=` desugar to `Comparable.compare(a, b)` followed by an
// Ordering match (std/comparable.nomi's own doc comment). Two switches that
// must agree can drift, and an agreement test between two spellings passes
// when both are wrong the same way.
//
// So OrderingTag is the one place a variant name is paired with a tag, and
// OrderingRank is the one place a tag is paired with -1/0/1. Neither has a
// second encoding to drift from.

// Tag values, written out rather than left implicit at the construction sites
// because they are the one thing a reader has to be able to check against
// std/comparable.nomi by eye. Declaration order there is Less, Equal, Greater.
const (
	// TagLess, TagEqual and TagGreater are Ordering's variants, in
	// declaration order. 0 is TagInvalid (prelude.go), shared by every enum
	// rt declares.
	TagLess    uint8 = 1
	TagEqual   uint8 = 2
	TagGreater uint8 = 3
)

// Ordering is Nomi's `std/comparable.Ordering`: `Less`, `Equal` or `Greater`.
//
// Tag is exported because generated code is in another Go package and has to
// write the composite literal and read the tag back in a `case`.
type Ordering struct {
	// Tag is 1 for Less, 2 for Equal, 3 for Greater. 0 means never
	// constructed.
	Tag uint8
}

// OrderingTag is the tag a variant NAME names, and the only place that pairing
// exists. TagInvalid for anything else, which is what keeps an unexpected
// variant ranking as Equal while still being distinguishable from a real
// Equal.
func OrderingTag(variant string) uint8 {
	switch variant {
	case "Less":
		return TagLess
	case "Equal":
		return TagEqual
	case "Greater":
		return TagGreater
	}
	return TagInvalid
}

// OrderingRank maps an Ordering onto -1, 0, 1, which is what turns a compare
// result into a `<` / `>` / `<=` / `>=` answer: the operator is then Go's own
// comparison against 0 and needs no second table.
func OrderingRank(o Ordering) int64 {
	switch o.Tag {
	case TagLess:
		return -1
	case TagGreater:
		return 1
	}
	return 0
}

// Direction is Nomi's `std/comparable.Direction`: `Ascending` or `Descending`.
//
// The modifier on `Iter.sort` / `Iter.sort_by`, and here for the same reason
// Ordering is: it is declared in a std module and named from user modules, so
// no one module can own it. It carries no rank — a direction is not a
// comparison result, and giving it one would invite a caller to multiply the
// two, which is not how std spells descending order.
type Direction struct {
	// Tag is 1 for Ascending, 2 for Descending. 0 means never constructed.
	Tag uint8
}

// Tag values for Direction, in std/comparable.nomi's declaration order.
const (
	TagAscending  uint8 = 1
	TagDescending uint8 = 2
)
