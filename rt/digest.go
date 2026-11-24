package rt

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
)

// std/hash's constant-time comparison and PBKDF2. Its digests, MACs, HKDF
// and Hasher are internal/stdhash's, beside BLAKE2, which rt does not import.

// EqualInConstantTime is std/hash's `equal_in_constant_time`: whether a and
// b hold the same bytes, taking a time that depends on their lengths and
// not on where they first differ.
func EqualInConstantTime(a, b Bytes) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// PBKDF2SHA256 is std/hash's `pbkdf2_sha256`: PBKDF2 with HMAC-SHA256
// (RFC 8018) over password and salt, iterations rounds, length bytes long.
// std declares both counts PositiveInts of any size, and any length is
// derived: RFC 8018's own limit, (2^32 - 1) blocks of 32 bytes, is past
// what memory holds, so Go's error for it is a backstop. A count past int64
// saturates: as many iterations never end either, and as long a key is past
// that limit.
func PBKDF2SHA256(password, salt Bytes, iterations, length Int) Bytes {
	key, err := pbkdf2.Key(sha256.New, string(password), []byte(salt), int(iterations.Saturated()), int(length.Saturated()))
	if err != nil {
		Trap("hash.pbkdf2_sha256: " + err.Error())
	}
	return Bytes(key)
}
