package rt

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256 is the SHA-256 digest of data, 32 bytes: std/hash's `sha256`.
func SHA256(data Bytes) Bytes {
	sum := sha256.Sum256([]byte(data))
	return Bytes(sum[:])
}

// SHA256Hex is the SHA-256 digest of data as 64 lowercase hexadecimal
// digits: std/hash's `sha256_hex`.
func SHA256Hex(data Bytes) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
