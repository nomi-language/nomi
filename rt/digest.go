package rt

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
)

// SHA256 is the SHA-256 digest of data, 32 bytes: std/hash's `sha256`.
func SHA256(data Bytes) Bytes {
	sum := sha256.Sum256([]byte(data))
	return Bytes(sum[:])
}

// SHA512 is the SHA-512 digest of data, 64 bytes: std/hash's `sha512`.
func SHA512(data Bytes) Bytes {
	sum := sha512.Sum512([]byte(data))
	return Bytes(sum[:])
}

// MD5 is the MD5 digest of data, 16 bytes: std/hash's `md5`, for formats
// that still name it. MD5 is broken for security.
func MD5(data Bytes) Bytes {
	sum := md5.Sum([]byte(data))
	return Bytes(sum[:])
}

// SHA1 is the SHA-1 digest of data, 20 bytes: std/hash's `sha1`, for
// formats that still name it. SHA-1 is broken for security.
func SHA1(data Bytes) Bytes {
	sum := sha1.Sum([]byte(data))
	return Bytes(sum[:])
}

// HMACSHA256 is the HMAC-SHA256 of data under key, 32 bytes: std/hash's
// `hmac_sha256`.
func HMACSHA256(key, data Bytes) Bytes {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return Bytes(mac.Sum(nil))
}

// HMACSHA512 is the HMAC-SHA512 of data under key, 64 bytes: std/hash's
// `hmac_sha512`.
func HMACSHA512(key, data Bytes) Bytes {
	mac := hmac.New(sha512.New, []byte(key))
	mac.Write([]byte(data))
	return Bytes(mac.Sum(nil))
}

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
