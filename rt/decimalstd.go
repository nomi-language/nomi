package rt

import (
	"math"
	"math/big"
	"strconv"
)

// `std/decimal`'s host surface, once.
//
// What is here is only the RULE: the arguments are already the right Go types.
//
// One implementation matters more for Decimal than for Int, because these
// rules are not arithmetic that a second implementation would get visibly
// wrong -- they are ROUNDING and CONVERSION policies, and a second
// implementation gets those wrong quietly. `Decimal.from_float(0.1, 2,
// HalfEven)` is `Some(0.10d)` only because the float's exact binary value is
// taken as a rational first; an implementation that went through `%g` would
// answer `Some(0.10d)` too, and would answer differently at some scale nobody
// tested.
//
// The VM calls THESE functions through the generated host adapters, so the
// fault TEXT has one home too: a fault names the std function it came from
// (`Decimal.divide: decimal: division by zero`). decimalFault is that prefix,
// once.

// DecimalFromInt is `Decimal.from_int`: exact, scale 0, total.
func DecimalFromInt(n int64) Decimal {
	return Decimal{Mantissa: big.NewInt(n), Scale: 0}
}

// DecimalFromString is `Decimal.from_string`, and None is the whole point: it
// parses PLAIN base-10 only, so "1e5" is None rather than 100000.
func DecimalFromString(s string) Maybe[Decimal] {
	d, err := ParseDecimal(s)
	if err != nil {
		return None[Decimal]()
	}
	return Some(d)
}

// DecimalToInt is `Decimal.to_int`: truncate toward zero, then range-check.
//
// THAT ORDER IS OBSERVABLE. `-9223372036854775808.5` is below Int64's minimum,
// yet truncating toward zero lands exactly on it, so this answers Some where a
// range-check-first implementation answers None. A half-step past a boundary is
// the only input that can tell the two apart, which is why one is pinned.
func DecimalToInt(d Decimal) Maybe[int64] {
	trunc, _ := d.Round(0, RoundDown)
	m := trunc.Mantissa
	if m == nil || !m.IsInt64() {
		return None[int64]()
	}
	return Some(m.Int64())
}

// DecimalToFloat is `Decimal.to_float`: total and lossy by nature.
//
// Via the canonical Display string rather than mantissa/2^k arithmetic, so the
// binary64 result is the one `strconv.ParseFloat` would give for the number as
// written. Out-of-range magnitudes become ±Inf: ParseFloat reports a range
// error with the infinity already in hand, and the Float island has Inf, so the
// error is deliberately dropped rather than turned into a None this signature
// has no room for.
func DecimalToFloat(d Decimal) float64 {
	f, _ := strconv.ParseFloat(d.Display(), 64)
	return f
}

// DecimalFromFloat is `Decimal.from_float` at an explicit scale and mode.
//
// NaN and ±Inf are None. Everything else goes through the float's EXACT binary
// value as a rational, which is the only way the answer is honest: 0.1 as a
// float64 is 0.1000000000000000055511151231257827, and rounding THAT at scale 2
// with HalfEven is what yields 0.10 rather than something that happens to look
// right. A `%g` round-trip would agree here and disagree somewhere unlisted.
//
// Traps only for RoundUnnecessary on an inexact value, which is that mode's
// entire contract.
func DecimalFromFloat(f float64, scale int64, mode RoundingMode) Maybe[Decimal] {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return None[Decimal]()
	}
	rat := new(big.Rat).SetFloat64(f)
	if rat == nil {
		return None[Decimal]()
	}
	rounded, err := RatToDecimal(rat).Round(int32(scale), mode)
	if err != nil {
		decimalFault("Decimal.from_float", err)
	}
	return Some(rounded)
}

// DecimalDivide is `Decimal.divide`: the EXPLICIT-rounding division, as opposed
// to the `/` operator, which is exact-or-trap. Traps on a zero divisor and on
// RoundUnnecessary when the exact quotient does not fit at scale.
func DecimalDivide(a, b Decimal, scale int64, mode RoundingMode) Decimal {
	res, err := a.DivRound(b, int32(scale), mode)
	if err != nil {
		decimalFault("Decimal.divide", err)
	}
	return res
}

// DecimalRound is `Decimal.round`: quantize to exactly scale digits.
//
// INCREASING the scale pads and is always exact, which is why RoundUnnecessary
// permits it -- the mode asserts that no information is lost, not that no
// digits move.
func DecimalRound(d Decimal, scale int64, mode RoundingMode) Decimal {
	res, err := d.Round(int32(scale), mode)
	if err != nil {
		decimalFault("Decimal.round", err)
	}
	return res
}

// decimalFault traps with a Decimal std function's fault: the function's Nomi
// name, then the arithmetic's own error.
func decimalFault(fn string, err error) { Trap(fn + ": " + err.Error()) }

// DecimalNormalize is `Decimal.normalize`: minimal scale, same value.
func DecimalNormalize(d Decimal) Decimal { return d.Normalize() }

// DecimalScale is `Decimal.scale`: the number of fractional digits, which is
// display-significant even though equality ignores it.
func DecimalScale(d Decimal) int64 { return int64(d.Scale) }

// DecimalToString is `Decimal.to_string`, i.e. `impl Display for Decimal`:
// scale-preserving, so 1.50d renders "1.50" and not "1.5".
func DecimalToString(d Decimal) string { return d.Display() }

// DecimalInspect is `impl Debug for Decimal`: the canonical form plus `d`.
//
// The suffix is DELIBERATE and must not be shared with Display. It is what
// makes a Decimal distinguishable from a Float in a REPL and what makes the
// output paste back as a literal. std/decimal.nomi writes this as Nomi
// (`to_string(d) + "d"`); this is the same rule for Go callers.
func DecimalInspect(d Decimal) string { return d.Display() + "d" }

// DecimalCompare is `impl Comparable for Decimal`, in the Ordering the Nomi
// interface returns rather than the -1/0/1 Decimal.Compare gives.
func DecimalCompare(a, b Decimal) Ordering {
	switch a.Compare(b) {
	case -1:
		return Ordering{Tag: TagLess}
	case 0:
		return Ordering{Tag: TagEqual}
	default:
		return Ordering{Tag: TagGreater}
	}
}

// DecimalHash is `impl Hashable for Decimal`, as the Nomi `Int` the interface
// returns. HashDecimal's uint64 reinterpreted, not truncated.
func DecimalHash(d Decimal) int64 { return int64(HashDecimal(d)) }

// RatToDecimal renders an exact rational as a Decimal carrying enough
// fractional digits that a later Round to a smaller scale is correct.
//
// A float64's exact value is a dyadic rational, so its base-10 expansion
// terminates, and it terminates within den.BitLen() digits because each halving
// adds exactly one decimal digit. So the division below is exact and no
// rounding-relevant digit is lost before the caller requantizes.
func RatToDecimal(r *big.Rat) Decimal {
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())
	scale := int32(den.BitLen())
	if scale < 0 {
		scale = 0
	}
	scaled := new(big.Int).Mul(num, pow10(scale))
	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(scaled, den, rem)
	return Decimal{Mantissa: q, Scale: scale}
}
