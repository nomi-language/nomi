package rt

// The Go types behind `std/int`'s two refinement newtypes.
//
// # `NonZeroInt` and `PositiveInt` are refinements, not handles
//
// std/int.nomi declares `pub opaque type NonZeroInt Int` and
// `pub opaque type PositiveInt Int`. Both are proofs carried in the type: a
// NonZeroInt is an Int that has been checked against 0, and `Int.divide` /
// `Int.modulo` take one so the division cannot trap. Nothing about them is a
// runtime handle, so — exactly like `Duration` and `Instant` in opaque.go — the
// representation is a Go defined type over int64.
//
// They are declared here for the reason opaque.go gives: a named type's
// identity in internal/irbuild is the *typeDef its declaration produced, and a
// std type needs one Go type every module's code can name.
//
// No extern anywhere takes or returns either of them, which is a difference
// from opaque.go. Every function over them in std is ordinary Nomi —
// `Int.to_non_zero(n)` is
// `if n == 0 { None } else { Some(NonZeroInt(n)) }`, and `Int.divide` is a
// destructure plus `/` — so what the Go types are needed for is the signature of
// those Nomi bodies, not an implementation. Those signatures also need
// `Maybe<T>`'s package-neutral identity (internal/irbuild/stdprelude.go).
//
// # No Go constructor, and that is deliberate
//
// A `NewNonZeroInt(int64) (NonZeroInt, bool)` here would be a second encoding of
// the refinement rule, and std/int.nomi already has the first one in Nomi. If rt
// implemented it too, the two could drift, and an agreement test between the
// two spellings passes when both are wrong the same way. So there is one
// implementation, it is the Nomi source, and the VM runs it.

// NonZeroInt is Nomi's `std/int.NonZeroInt`: an Int proven not to be 0.
//
// The proof is established by `Int.to_non_zero` in std/int.nomi and by nothing
// here; a zero-valued NonZeroInt is only reachable by a Go caller writing one,
// which no generated code does.
type NonZeroInt int64

// PositiveInt is Nomi's `std/int.PositiveInt`: an Int proven to be > 0.
type PositiveInt int64
