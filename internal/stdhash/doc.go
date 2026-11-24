// Package stdhash is std/hash's digests and MACs: the hash functions behind
// `Algorithm`, one-shot and streaming (`Hasher`), HMAC and HKDF (RFC 5869,
// over Go's crypto/hkdf) over each, and the SHAKE extendable-output
// functions. SHA-2, SHA-3, SHA-1 and MD5 are Go's
// crypto packages; BLAKE2 is golang.org/x/crypto's, which is why this is not
// in rt. std/hash's PBKDF2 and constant-time comparison are rt's
// (rt/digest.go).
//
// # Algorithms
//
// std/hash.nomi passes an `Algorithm` across as its position in the enum's
// declaration (its private `code`), and algorithms is the table it indexes:
// the one place a code becomes a hash function. Every entry point here,
// one-shot or streaming, plain, HMAC or HKDF, makes its hash from that
// table, so a digest, an HMAC, an HKDF key and a Hasher of one algorithm
// cannot disagree.
// TestAlgorithmsMatchTheEnum holds the table to the declaration, name by name.
//
// # A Hasher is a value
//
// rt.Hasher wraps a hash.Hash that is never written once the Hasher exists.
// `update` clones the state and writes the chunk into the clone, and `finish`
// sums a clone, since an HMAC's Sum rewrites its outer hash. So a Hasher kept
// and updated twice gives two digests, two tasks may share one, and the VM's
// copying and in-place machines run a program alike: there is no in-place
// write for ownership to license. The price is one copy of the state per
// call, a few hundred bytes at most (SHA-3's is 200 bytes of sponge plus its
// buffer), which a chunk of any useful size dwarfs. Forking a Hasher costs
// nothing beyond that: every update is already a fork.
//
// Every hash here clones through hash.Cloner, and maker wraps each in
// cloning to make it one: through the hash's own Clone where it has one,
// and through the state it marshals where it has none (x/crypto's BLAKE2).
// Go's HMAC clones by cloning its inner and outer hashes, and only the FIPS
// ones it unwraps them to, which leaves out SHA-3; over a cloning it clones
// for every algorithm.
//
// Every function here computes its answer from its operands alone, so every
// row is Pure, and none is Seeded: these are fixed functions, the same in
// every run. Their cost grows with an operand's size, which is not what
// Unbounded marks, except SHAKE's, whose length is a value. HKDF's length
// is a value too, but one past 255 blocks is an Err, so its cost is bounded.
package stdhash
