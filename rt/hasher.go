package rt

import "hash"

// Hasher is std/hash's `Hasher`: a digest or a MAC part way through its
// input, as a value. internal/stdhash builds it and runs it; rt carries it
// and never opens it, since BLAKE2 is golang.org/x/crypto's, which rt does
// not import.
//
// The state a Hasher holds is never written once the Hasher exists:
// `Hasher.update` writes into a clone, and `Hasher.finish` sums a clone, so
// a program may keep a Hasher, update it twice or finish it twice, and two
// tasks may share one.
type Hasher struct{ state hash.Hash }

// NewHasher wraps state as a value. The caller never writes state again.
func NewHasher(state hash.Hash) Hasher { return Hasher{state: state} }

// State is the hash the value wraps, which the caller only reads.
func (h Hasher) State() hash.Hash { return h.state }
