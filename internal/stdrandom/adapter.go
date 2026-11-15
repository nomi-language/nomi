// Package random supplies std/random's splitmix64 PRNG and OS-entropy seed.
//
// The generator state is std/random's opaque `Seed`, an Int, and the
// arithmetic below is defined on its splitmix64 register, a uint64. A Seed
// crosses as an Int of any size (rt.Int) and its register is its low 64
// bits, two's complement (register), so `Seed.from_int(n)` and
// `Seed.from_int(n + 2^64)` step alike; the state a draw returns is the
// register reinterpreted as an int64, never a numeric change.
//
// A draw produces two results (the value and the advanced state), which the
// FFI boundary carries as a Nomi tuple: a Go multiple return projects
// element-by-element onto the tuple the Nomi declaration spells.
package stdrandom

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/nomi-language/nomi/rt"
)

// splitmix64 advances `state` and returns (scrambled output, next state).
// Reference: Vigna's splitmix64 (https://prng.di.unimi.it/splitmix64.c).
func splitmix64(state uint64) (out uint64, next uint64) {
	state += 0x9E3779B97F4A7C15
	z := state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	return z, state
}

// FFIBetween returns an unbiased integer in the inclusive range [from, to]
// and the advanced state. The caller's range is a BoundedRange, so from <=
// to. A range whose bounds fit int64 is drawn by
// between64; a wider one by bigBetween. A reversed range answers from with
// the state unchanged rather than inventing a value.
func FFIBetween(seed, from, to rt.Int) (value rt.Int, next int64) {
	state := register(seed)
	f, fromFits := from.Int64()
	t, toFits := to.Int64()
	if fromFits && toFits {
		v, s := between64(state, f, t)
		return rt.SmallInt(v), s
	}
	return bigBetween(state, from.Big(), to.Big())
}

// bigBetween draws a value in [lo, hi] for bounds of any size: as many 64-bit
// outputs as the width needs, cut to the width's bit length, and drawn again
// when past the width, which removes the bias a remainder would add. Each try
// succeeds with probability above one half.
func bigBetween(state int64, lo, hi *big.Int) (rt.Int, int64) {
	if lo.Cmp(hi) > 0 {
		return rt.BigIntOf(lo), state
	}
	width := new(big.Int).Sub(hi, lo)
	width.Add(width, big.NewInt(1))
	n := width.BitLen()
	mask := new(big.Int).Lsh(big.NewInt(1), uint(n))
	mask.Sub(mask, big.NewInt(1))
	s := uint64(state)
	r, word := new(big.Int), new(big.Int)
	for {
		r.SetUint64(0)
		for drawn := 0; drawn < n; drawn += 64 {
			var z uint64
			z, s = splitmix64(s)
			r.Lsh(r, 64)
			r.Or(r, word.SetUint64(z))
		}
		r.And(r, mask)
		if r.Cmp(width) < 0 {
			return rt.BigIntOf(r.Add(r, lo)), int64(s)
		}
	}
}

// between64 is FFIBetween for int64 bounds. The span is counted in uint64, so
// a range wider than int64 can hold, up to all of it, draws without
// overflow: a range of all 2^64 values takes the raw output, and any other
// draws an offset below its width by rejection sampling, which removes modulo
// bias, and adds it to from modulo 2^64.
func between64(state, from, to int64) (value int64, next int64) {
	if from > to {
		return from, state
	}
	span := uint64(to) - uint64(from) // the width minus one, exact in uint64
	if span == ^uint64(0) {
		z, advanced := splitmix64(uint64(state))
		return int64(z), int64(advanced)
	}
	offset, s := below(uint64(state), span+1)
	return int64(uint64(from) + offset), int64(s)
}

// below is an unbiased integer in [0, n) for n >= 1 and the advanced state.
func below(s, n uint64) (uint64, uint64) {
	limit := ^uint64(0) - (^uint64(0) % n) // largest multiple of n that fits
	for {
		z, advanced := splitmix64(s)
		s = advanced
		if z < limit {
			return z % n, s
		}
	}
}

// FFIUnitFloat returns a float in [0.0, 1.0) and the advanced state, using the
// top 53 bits (the float64 mantissa width).
func FFIUnitFloat(seed rt.Int) (value float64, next int64) {
	z, advanced := splitmix64(uint64(register(seed)))
	return float64(z>>11) / float64(uint64(1)<<53), int64(advanced)
}

// FFIFromOS reads a fresh generator state from OS entropy.
func FFIFromOS() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("OS entropy unavailable: %w", err)
	}
	return int64(binary.LittleEndian.Uint64(b[:])), nil
}

// FFISecureBytes is std/random's `secure_bytes`: count bytes from the
// operating system's cryptographically secure source. crypto/rand.Read does
// not fail since Go 1.24, so the result needs no error. std declares count a
// NonNegativeInt.
//
// A count past int64 is more bytes than memory holds, and traps naming it.
func FFISecureBytes(count rt.Int) rt.Bytes {
	n, ok := count.Int64()
	if !ok {
		rt.Trap(fmt.Sprintf("random.secure_bytes: %s bytes are more than memory holds", count))
	}
	b := make([]byte, n)
	rand.Read(b)
	return rt.Bytes(b)
}

// register is a Seed's splitmix64 register: its low 64 bits, two's
// complement, as an int64.
func register(seed rt.Int) int64 {
	if v, ok := seed.Int64(); ok {
		return v
	}
	low := new(big.Int).And(seed.Big(), new(big.Int).SetUint64(^uint64(0)))
	return int64(low.Uint64())
}
