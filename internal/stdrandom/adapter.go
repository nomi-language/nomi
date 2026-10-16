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

// FFIBelow returns an unbiased integer in [0, n) and the advanced state.
// Rejection sampling removes modulo bias. n < 1 has no valid answer; the
// callers in std/random pass `to - from + 1` and never reach it, so it
// reports the state unchanged with a zero value rather than inventing one.
func FFIBelow(state, n int64) (value int64, next int64) {
	if n < 1 {
		return 0, state
	}
	s := uint64(state)
	un := uint64(n)
	limit := ^uint64(0) - (^uint64(0) % un) // largest multiple of n that fits
	for {
		z, advanced := splitmix64(s)
		s = advanced
		if z < limit {
			return int64(z % un), int64(s)
		}
	}
}

// FFIRequireIntRange is std/random's `require_int_range`, the check
// `Generator.int` makes before it builds a generator: it traps unless
// from <= to. The caller chose both bounds and can compare them first, so a
// reversed range is a broken promise (spec §17), not an Err.
func FFIRequireIntRange(from, to int64) rt.Unit {
	if from > to {
		rt.Trap(IntRangeText(from, to))
	}
	return rt.Unit{}
}

// IntRangeText is the trap a reversed `Generator.int` range raises:
// `Generator.int: from 6 is greater than to 1`.
func IntRangeText(from, to int64) string {
	return fmt.Sprintf("Generator.int: from %d is greater than to %d", from, to)
}

// FFIRequireFloatRange is std/random's `require_float_range`, the check
// `Generator.float` makes: it traps unless from < to, which also refuses a
// NaN bound, since NaN is less than nothing.
func FFIRequireFloatRange(from, to float64) rt.Unit {
	if !(from < to) {
		rt.Trap(FloatRangeText(from, to))
	}
	return rt.Unit{}
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
