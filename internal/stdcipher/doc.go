// Package stdcipher is std/cipher/host and std/key_exchange/host: the
// authenticated encryption std::cipher seals and opens with, unauthenticated
// AES-CTR, and the elliptic-curve Diffie-Hellman std::key_exchange agrees
// keys with. AES-GCM and AES-CTR are Go's crypto/aes and crypto/cipher,
// ChaCha20-Poly1305 and XChaCha20-Poly1305 golang.org/x/crypto's, AES-CBC
// with HMAC (RFC 7518, section 5.2) this package's own cbcHMAC over
// crypto/aes and crypto/hmac, and the key exchange Go's crypto/ecdh. Each
// AEAD is a cipher.AEAD that `aead` makes by its engine number, so seal and
// open have one path for all of them; cbcHMAC checks its tag before it
// decrypts, and plain CBC never leaves it.
//
// This is the one AEAD in the implementation: std::sessions seals its
// cookies with std::cipher, so nothing else encrypts.
//
// The Nomi side decides every length. std/cipher.nomi holds the key, nonce
// and tag length of each algorithm, checks a key when it makes one and a
// nonce and a sealed value's length before it crosses, and draws a random
// nonce with `random.secure_bytes`, so seal and open here are given lengths
// that fit and compute from their operands alone. A length that does not
// fit is a broken invariant of std/cipher, and the adapter reports Go's
// panic for it. An engine number names the construction only: AES-GCM
// takes a 16- or 32-byte key and Go's crypto/aes picks AES-128 or AES-256 by
// its length.
//
// A key exchange crosses as a curve number and the curve's standard
// encodings: a private key as its scalar (32 bytes for X25519, the curve's
// order's length for a NIST curve), a public key as 32 bytes for X25519 and
// an uncompressed SEC 1 point for a NIST curve. std/key_exchange.nomi checks
// a key with `private_is_valid` or `public_is_valid` when it makes one, so the
// other functions are given keys that parse.
//
// Every row is Pure except `key_exchange/host.generate`, which reads the
// operating system's entropy for the program's new key: an effect, Outside,
// as a signing key's generation is (internal/stdlibbindings package doc,
// "Effects and World"). No row's cost grows with an operand's value, so none
// is Unbounded.
package stdcipher
