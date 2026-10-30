// Package random supplies std/random's splitmix64 PRNG and OS-entropy seed.
//
// The generator state is carried across the boundary as a plain int64 — the
// same bit pattern std/random's opaque `Seed` wraps — because a Nomi `Int` is
// an int64 and the arithmetic below is defined on uint64. Every conversion is
// a reinterpretation, never a numeric change.
//
// A draw produces two results (the value and the advanced state), which the
// FFI boundary carries as a Nomi tuple: a Go multiple return projects
// element-by-element onto the tuple the Nomi declaration spells.
package stdrandom

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
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
// and the advanced state. The caller has checked from <= to
// (FFIRequireIntRange). A range whose bounds fit int64 is drawn by
// between64; a wider one by bigBetween. A reversed range answers from with
// the state unchanged rather than inventing a value.
func FFIBetween(state int64, from, to rt.Int) (value rt.Int, next int64) {
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

// FFIRequireIntRange is std/random's `require_int_range`, the check
// `Generator.int` makes before it builds a generator: it traps unless
// from <= to. The caller chose both bounds and can compare them first, so a
// reversed range is a broken promise (spec §17), not an Err.
func FFIRequireIntRange(from, to rt.Int) rt.Unit {
	if rt.IntCompare(from.Value(), to.Value()) > 0 {
		rt.Trap(IntRangeText(from, to))
	}
	return rt.Unit{}
}

// IntRangeText is the trap a reversed `Generator.int` range raises:
// `Generator.int: from 6 is greater than to 1`.
func IntRangeText(from, to rt.Int) string {
	return fmt.Sprintf("Generator.int: from %s is greater than to %s", from, to)
}

// FFIRequireFloatRange is std/random's `require_float_range`, the check
// `Generator.float` makes: it traps unless both bounds are finite and
// from < to, which also refuses a NaN bound, since NaN is less than nothing.
// An infinite bound has no uniform draw, so it is refused rather than
// answered with infinities or NaN.
func FFIRequireFloatRange(from, to float64) rt.Unit {
	if math.IsInf(from, 0) || math.IsInf(to, 0) {
		rt.Trap(FloatBoundsText(from, to))
	}
	if !(from < to) {
		rt.Trap(FloatRangeText(from, to))
	}
	return rt.Unit{}
}

// FloatBoundsText is the trap a `Generator.float` with an infinite bound
// raises: `Generator.float: bounds must be finite, got -Inf and 1.0`.
func FloatBoundsText(from, to float64) string {
	return fmt.Sprintf("Generator.float: bounds must be finite, got %s and %s", rt.FormatFloat(from), rt.FormatFloat(to))
}

// FloatRangeText is the trap an empty `Generator.float` range raises:
// `Generator.float: from 2.5 is not less than to 1.5`. The bounds read as
// Nomi prints a Float.
func FloatRangeText(from, to float64) string {
	return fmt.Sprintf("Generator.float: from %s is not less than to %s", rt.FormatFloat(from), rt.FormatFloat(to))
}

// FFIUnitFloat returns a float in [0.0, 1.0) and the advanced state, using the
// top 53 bits (the float64 mantissa width).
func FFIUnitFloat(state int64) (value float64, next int64) {
	z, advanced := splitmix64(uint64(state))
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
