package rt

import "math"

// The STRUCTURAL hash that buckets Map keys — and the one thing about it that
// matters more than the algorithm: it is not `Hashable.hash`.
//
// # Why not `Hashable.hash`, which is right there and already means "hash"
//
// `Hashable` is a Nomi interface (std/hashable.nomi) and therefore
// USER-OVERRIDABLE: `impl Hashable for Point { fn hash(p: Point): Int { 0 } }`
// is legal Nomi, and so is one that disagrees with `impl Equatable for Point`.
// A Map keyed on `Hashable.hash` would then go wrong SILENTLY and only for
// programs that got the hash/equality law wrong: two keys Equal considers
// equal would land in different buckets and become unretrievable. `Map.get`
// returning None for a key that is present is a wrong answer, and no test over
// well-behaved keys would ever see it.
//
// Hence: structural, recursive, un-overridable, with no user hook. Equality
// and this hash are defined over the same set of kinds, because a hash without
// a matching equality is a bucket nobody can search.
//
// # One FNV in this package, not two
//
// HashString is `uint64(StringHash(s))` and nothing else. StringHash is
// Nomi's `String.hash` — FNV-1a 64-bit over the UTF-8 bytes, reinterpreted as
// int64 so the full bit pattern survives (stdlib.go) — and a second FNV loop
// here would be two spellings of one rule, which is how this project has
// already lost time five times. HashFloat likewise goes through FloatBits.
//
// Structural hashes choose map buckets; user-dispatched Hashable.hash remains
// a separate operation. Equality implies equal hashes.

// HashMix folds b into a for structural hashes.
func HashMix(a, b uint64) uint64 { return a*0x100000001b3 + b }

// HashInt is the structural hash of an Int: the value's own bits.
//
// Which is also Nomi's `impl Hashable for Int` (std/int.nomi:186, `n`). A
// low-entropy hash in the low bits is fine here
// and deliberate: the trie indexes from the BOTTOM up, so consecutive Int keys
// — overwhelmingly the common Int-keyed shape — spread across sibling slots of
// one node instead of colliding down a path.
func HashInt(v int64) uint64 { return uint64(v) }

// HashString is the structural hash of a String.
//
// Delegates to StringHash, which is Nomi's `String.hash`. Same bytes, same
// algorithm, ONE implementation: see the file comment.
func HashString(s string) uint64 { return uint64(StringHash(s)) }

// HashFloat is the structural hash of a Float, agreeing with EqFloat.
//
// Two normalizations, and each one is a key that could otherwise not be found
// again:
//
//   - NaN. Nomi's `==` on Float is REFLEXIVE (EqFloat), so a NaN key must be
//     retrievable from the map it was inserted into — and NaN has 2^52 encodings.
//     They all hash to one canonical bucket.
//   - Signed zero. `-0.0 == 0.0` in Nomi and in Go, and their bit patterns
//     differ, so they must collapse.
//
// This is `impl Hashable for Float` (std/float.nomi:154) taken through
// FloatBits.
func HashFloat(v float64) uint64 {
	switch {
	case math.IsNaN(v):
		return 0x7ff8000000000001 // one canonical NaN bucket
	case v == 0:
		return 0 // collapses -0.0 into +0.0
	default:
		return uint64(FloatBits(v))
	}
}

// HashTupleSeed starts a tuple's field-wise hash fold, which a caller that
// knows the tuple's arity and field types assembles inline.
//
// A named constant rather than a magic number at each site: the fold is
// `HashMix(HashMix(HashTupleSeed, h0(v.F0)), h1(v.F1))`. Non-zero so that a 1-field tuple of a
// zero-hashing part is distinguishable from the part itself.
const HashTupleSeed = uint64(0x9e3779b97f4a7c15)

// HashUnit is the structural hash of Unit — one inhabitant, one bucket.
func HashUnit(Unit) uint64 { return 0x1 }

// HashVector is the STRUCTURAL hash of a Vector. It seeds 0x200 where
// HashListCells seeds 0x100 and is otherwise the same fold. One constant apart,
// and the constant is what keeps a List and a Vector of the same elements in
// different buckets.
//
// NOT `Hashable.hash`, which is std's `Iter.reduce(v, |acc = 19, x|
// wrapping_add(wrapping_mul(acc, 31), Hashable.hash(x)))`, a USER-VISIBLE number
// a Nomi program can print. This one is the map-key identity `rt.Map` buckets on,
// and it must agree with Hash (anyvalue.go) rather than with std, because an
// entry written through one and read through the other has to land in the same
// place.
//
// It folds the VISIBLE WINDOW — `Items[Start+i]` for `i` in `[0, Len)` — which is
// the property that makes it agree with VectorEqual. A view and a freshly built
// vector holding the same elements are Equal, so they must hash alike; folding
// the backing slice instead would bucket a tail by elements it cannot see, and
// the entry would be written and then unreachable.
func HashVector[T any](v Vector[T], h func(T) uint64) uint64 {
	acc := uint64(0x200)
	for i := range v.Len {
		acc = HashMix(acc, h(v.Items[v.Start+i]))
	}
	return acc
}
