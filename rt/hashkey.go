package rt

import (
	"crypto/rand"
	"encoding/binary"
	"math/bits"
	"os"
	"testing"
)

// hashSeedEnv names the environment variable that fixes the process's hash
// seed. Any text fixes it, and the same text gives the same hashes in every
// process.
const hashSeedEnv = "NOMI_HASH_SEED"

// testHashSeed is the seed a test binary takes when hashSeedEnv is unset.
const testHashSeed = "nomi-test"

// hashKey is the process's 128-bit hash key, and the two words HashMix
// folds with, derived from it. It is set once, when the package
// initializes, before any value is hashed (loadHashKey), and only a test
// of this package sets it again (setHashKey).
var hashKey = loadHashKey()

type hashKeys struct {
	// sip0 and sip1 key SipHash over a String's or a Bytes' octets.
	sip0, sip1 uint64
	// mix0 and mix1 key HashMix.
	mix0, mix1 uint64
}

// loadHashKey is the process's key: the seed hashSeedEnv names, a fixed
// seed in a test binary that names none, which it sets in its own
// environment so every process it starts inherits it (as
// internal/artifactcache hands down its cache directory), and otherwise
// 128 bits from crypto/rand. A built executable, the wasip1 reactor and
// the browser build each run this when they start.
func loadHashKey() hashKeys {
	seed, fixed := os.LookupEnv(hashSeedEnv)
	if !fixed && testing.Testing() {
		seed, fixed = testHashSeed, true
		_ = os.Setenv(hashSeedEnv, seed)
	}
	if fixed {
		return keysFromSeed(sipHash13(0, 0, seed), sipHash13(0, 1, seed))
	}
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand.Read)
	return keysFromSeed(binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:]))
}

// keysFromSeed spreads a 128-bit seed into SipHash's key and HashMix's,
// so the two never share a word.
func keysFromSeed(s0, s1 uint64) hashKeys {
	return hashKeys{
		sip0: s0,
		sip1: s1,
		mix0: splitMix64(s0 ^ 0x243f6a8885a308d3),
		mix1: splitMix64(s1 ^ 0x13198a2e03707344),
	}
}

// setHashKey replaces the process's key and recomputes the hashes derived
// from it at initialization. Only this package's tests call it, between
// hashes, to run one program under two seeds.
func setHashKey(k hashKeys) {
	hashKey = k
	hashTrue, hashFalse = HashVariantHead("Bool", "True"), HashVariantHead("Bool", "False")
}

func splitMix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// sipHash13 is SipHash-1-3 of p under the key (k0, k1): one compression
// round per 8-byte block and three finalization rounds.
func sipHash13(k0, k1 uint64, p string) uint64 {
	v0 := k0 ^ 0x736f6d6570736575
	v1 := k1 ^ 0x646f72616e646f6d
	v2 := k0 ^ 0x6c7967656e657261
	v3 := k1 ^ 0x7465646279746573
	last := uint64(len(p)) << 56
	for len(p) >= 8 {
		m := uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24 |
			uint64(p[4])<<32 | uint64(p[5])<<40 | uint64(p[6])<<48 | uint64(p[7])<<56
		v3 ^= m
		v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
		v0 ^= m
		p = p[8:]
	}
	for i := len(p) - 1; i >= 0; i-- {
		last |= uint64(p[i]) << (8 * uint(i))
	}
	v3 ^= last
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0 ^= last
	v2 ^= 0xff
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	v0, v1, v2, v3 = sipRound(v0, v1, v2, v3)
	return v0 ^ v1 ^ v2 ^ v3
}

func sipRound(v0, v1, v2, v3 uint64) (uint64, uint64, uint64, uint64) {
	v0 += v1
	v1 = bits.RotateLeft64(v1, 13)
	v1 ^= v0
	v0 = bits.RotateLeft64(v0, 32)
	v2 += v3
	v3 = bits.RotateLeft64(v3, 16)
	v3 ^= v2
	v0 += v3
	v3 = bits.RotateLeft64(v3, 21)
	v3 ^= v0
	v2 += v1
	v1 = bits.RotateLeft64(v1, 17)
	v1 ^= v2
	v2 = bits.RotateLeft64(v2, 32)
	return v0, v1, v2, v3
}
