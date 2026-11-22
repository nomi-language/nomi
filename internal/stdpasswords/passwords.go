// Package stdpasswords is std/passwords/host: the key derivations and the
// bcrypt encoding std::passwords' Argon2id, Scrypt and Bcrypt hashers call,
// over golang.org/x/crypto. The Nomi side (std/passwords.nomi) checks each
// parameter's range, draws Argon2id's and scrypt's salts with
// std::random, writes and reads their PHC strings, and compares hashes in
// constant time with std::hash; only the derivations, and bcrypt's own
// salt, encoding and comparison, are here.
//
// Each derivation's cost is set by its parameters, not its operands' sizes,
// so its rows are Pure and Unbounded. bcrypt draws its salt itself
// (x/crypto has no bcrypt that takes one), so BcryptHash is an effect named
// by std::passwords; a bcrypt hash means the same whatever the salt was, so
// it acts on nothing outside the machine (internal/stdlibbindings package
// doc, "Effects and World").
package stdpasswords

import (
	"errors"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// Argon2id is RFC 9106's Argon2id, version 19, of password and salt, length
// bytes long, with passes passes over memoryKiB KiB in lanes lanes, and no
// secret or associated data. The Nomi side declares each parameter's range,
// so lanes is at most 255.
func Argon2id(password, salt []byte, passes, memoryKiB, lanes, length uint32) []byte {
	return argon2.IDKey(password, salt, passes, memoryKiB, uint8(lanes), length)
}

// Scrypt is RFC 7914's scrypt of password and salt, length bytes long, with
// the CPU and memory cost N = 2^logCost, block size blockSize and
// parallelism, or why those cannot derive a key: x/crypto refuses a block
// size and parallelism whose product is 2^30 or more, and a cost or block
// size whose memory, 128 * N * blockSize bytes, an int cannot count.
func Scrypt(password, salt []byte, logCost, blockSize, parallelism, length uint32) ([]byte, error) {
	return scrypt.Key(password, salt, 1<<logCost, int(blockSize), int(parallelism), int(length))
}

// BcryptHash is password hashed by bcrypt at cost with a random salt, as
// `$2a$<cost>$...` (x/crypto writes the 2a prefix), or why it cannot be: a
// password over 72 bytes, which bcrypt would cut short.
func BcryptHash(password []byte, cost uint32) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(password, int(cost))
	return string(hash), err
}

// BcryptVerify is whether password is the one stored was made from, compared
// in constant time, or why stored is not a bcrypt hash. Its cost is the one
// stored records.
func BcryptVerify(password []byte, stored string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(stored), password)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return false, nil
	}
	return false, err
}

// BcryptCost is the cost stored was made at, or nil when it is not a bcrypt
// hash.
func BcryptCost(stored string) *int64 {
	cost, err := bcrypt.Cost([]byte(stored))
	if err != nil {
		return nil
	}
	n := int64(cost)
	return &n
}
