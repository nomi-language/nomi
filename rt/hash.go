package rt

import (
	"math"
	"math/bits"
)

// The structural hash that buckets Map keys, and the hash `Hashable.hash`
// answers for a value the machine hashes by its shape: a type with a
// derived Hashable or with none, and std's List, Vector, Map and Set (Hash,
// Keys.Hash).
//
// # A written impl decides for its own type
//
// `Hashable` and `Equatable` are Nomi interfaces, so a program may write
// either, and may write them inconsistently. Equal and Hash ask the hooks
// (KeyHooks) about a record whose type has a written impl, at any depth, and
// a type whose Equatable is written and whose Hashable is not hashes by its
// type name alone, so every pair its equality calls equal shares a bucket.
// Everything else is structural, recursive and un-overridable. Equality and
// this hash are defined over the same set of kinds, because a hash without
// a matching equality is a bucket nobody can search. Equality implies equal
// hashes.
//
// # Keyed by the process
//
// Every hash here that is not an Int's, a Float's, a Byte's or Unit's is
// keyed by a 128-bit key the process draws when it starts (hashkey.go), so
// a party that chooses a program's keys, the names in a JSON object it
// decodes among them, cannot compute a set of keys that share a bucket. A
// String's and a Bytes' octets go through SipHash-1-3 under the key
// (StringHash), and every composite folds its parts' hashes through
// HashMix, a non-linear fold keyed by it. Within one process a value
// always hashes alike; between processes it differs, which the spec
// allows (§5 *Hashing and keys*). NOMI_HASH_SEED fixes the key, and a test
// binary fixes it for itself and every process it starts, so recorded
// output that prints a hash stays the same.
//
// An Int keeps its own bits and a Float its normalized bits (HashInt,
// HashFloat): two distinct machine Ints never share 64 bits, so no set of
// them collides, and the trie indexes from the low bits, so consecutive
// keys spread across sibling slots. An Int past int64 folds its words
// through HashMix (hashBig), so it is keyed.
//
// # Ordered parts fold in order; unordered ones add
//
// A tuple, a List, a Vector, an enum variant's payload and a distinct fold
// their parts in order through HashMix, so `[1, 2]` and `[2, 1]` hash
// apart. A Map's entries and a struct's fields add instead, because Map
// equality ignores insertion order and a struct's equality compares its
// fields by name whatever the two records' layouts (namedFieldsEqual). Each
// entry is first mixed whole through HashMix, its key's hash with its
// value's or a field's name with its value, so the sum is of well-mixed
// words, and `Point{x: 1, y: 2}` and `Point{x: 2, y: 1}` hash apart.
//
// # One string hash in this package
//
// HashString is `uint64(StringHash(s))` and nothing else. StringHash is
// Nomi's `String.hash` (stdlib.go), and HashBytes and BytesHash call it
// too, so one SipHash loop hashes every run of octets.

// HashMix folds b into a for structural hashes: the 128-bit product of a
// and b, each first xored with a word of the process's key, with its two
// halves xored together. It is non-linear and order-sensitive, so
// `HashMix(HashMix(s, x), y)` and `HashMix(HashMix(s, y), x)` differ, and
// without the key nobody can choose parts whose folds agree.
func HashMix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a^hashKey.mix0, b^hashKey.mix1)
	return hi ^ lo
}

// HashInt is the structural hash of an Int: the value's own bits.
//
// Which is also Nomi's `impl Hashable for Int` (std/int.nomi, `n`). A
// low-entropy hash in the low bits is fine here
// and deliberate: the trie indexes from the bottom up, so consecutive Int keys
// — overwhelmingly the common Int-keyed shape — spread across sibling slots of
// one node instead of colliding down a path. Two distinct int64s never share a
// hash, so no choice of Int keys makes a bucket.
func HashInt(v int64) uint64 { return uint64(v) }

// HashString is the structural hash of a String.
//
// Delegates to StringHash, which is Nomi's `String.hash`. Same bytes, same
// algorithm, one implementation: see the file comment.
func HashString(s string) uint64 { return uint64(StringHash(s)) }

// HashFloat is the structural hash of a Float, agreeing with EqFloat.
//
// Two normalizations, and each one is a key that could otherwise not be found
// again:
//
//   - NaN. Nomi's `==` on Float is reflexive (EqFloat), so a NaN key must be
//     retrievable from the map it was inserted into — and NaN has 2^52 encodings.
//     They all hash to one canonical bucket.
//   - Signed zero. `-0.0 == 0.0` in Nomi and in Go, and their bit patterns
//     differ, so they must collapse.
//
// This is `impl Hashable for Float` (std/float.nomi) taken through
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

// HashTupleSeed starts a tuple's field-wise hash fold:
// `HashMix(HashMix(HashTupleSeed, h0(v.F0)), h1(v.F1))`. Non-zero so that a
// 1-field tuple of a zero-hashing part is distinguishable from the part
// itself.
const HashTupleSeed = uint64(0x9e3779b97f4a7c15)

// HashUnit is the structural hash of Unit — one inhabitant, one bucket.
func HashUnit(Unit) uint64 { return 0x1 }

// HashVector is the structural hash of a Vector, and `Hashable.hash` of
// one. It seeds 0x200 where HashListCells seeds 0x100 and is otherwise the
// same fold. One constant apart, and the constant is what keeps a List and
// a Vector of the same elements in different buckets.
//
// It folds the visible window in order, which is the property that makes it
// agree with VectorEqual. A view and a freshly built vector holding the same
// elements are Equal, so they must hash alike; folding the trie behind a view
// instead would bucket a tail by elements it cannot see, and the entry would be
// written and then unreachable.
func HashVector[T any](v Vector[T], h func(T) uint64) uint64 {
	acc := uint64(0x200)
	VectorEachWhile(nil, v, func(_ *Frame, x T) bool {
		acc = HashMix(acc, h(x))
		return true
	})
	return acc
}
