// Package goside is the Go half of the Nomi module password_hashers:
// Argon2id and bcrypt from golang.org/x/crypto, which std may not use. The
// Nomi side (password_hashers.nomi) writes and reads Argon2id's PHC
// strings itself and compares hashes in constant time with std/hash; only
// the key derivations, and bcrypt's own encoding, are here.
package goside

import (
	"errors"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Argon2id is RFC 9106's Argon2id of password and salt, length bytes long,
// with time passes over memoryKiB KiB in threads lanes. The Nomi side keeps
// every parameter in range before it calls this.
func Argon2id(password, salt []byte, time, memoryKiB, threads, length int64) []byte {
	return argon2.IDKey(password, salt, uint32(time), uint32(memoryKiB), uint8(threads), uint32(length))
}

// BcryptHash is password hashed by bcrypt at cost, as `$2a$<cost>$...`
// (x/crypto writes the 2a prefix), or why it cannot be: a password over 72
// bytes, which bcrypt would cut short, or a cost outside 4 to 31.
func BcryptHash(password []byte, cost int64) (string, string) {
	hash, err := bcrypt.GenerateFromPassword(password, int(cost))
	if err != nil {
		return "", err.Error()
	}
	return string(hash), ""
}

// BcryptVerify is whether password is the one stored was made from, and
// why stored is not a bcrypt hash when it is not one.
func BcryptVerify(password []byte, stored string) (bool, string) {
	err := bcrypt.CompareHashAndPassword([]byte(stored), password)
	switch {
	case err == nil:
		return true, ""
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return false, ""
	}
	return false, err.Error()
}

// BcryptCost is the cost stored was made at, or -1 when it is not a bcrypt
// hash.
func BcryptCost(stored string) int64 {
	cost, err := bcrypt.Cost([]byte(stored))
	if err != nil {
		return -1
	}
	return int64(cost)
}
