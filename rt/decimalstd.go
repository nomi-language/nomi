package rt

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// `std/decimal`'s host surface, once.
//
// What is here is only the rule: the arguments are already the right Go types.
//
// One implementation matters more for Decimal than for Int, because these
// rules are not arithmetic that a second implementation would get visibly
// wrong -- they are rounding and conversion policies, and a second
// implementation gets those wrong quietly. `Float.to_decimal(0.1, 2,
// HalfEven)` is `Some(0.10d)` only because the float's exact binary value is
// taken as a rational first; an implementation that went through `%g` would
// answer `Some(0.10d)` too, and would answer differently at some scale nobody
// tested.
//
// The VM calls these functions through the generated host adapters, so the
// fault text has one home too: a fault names the std function it came from
// (`Decimal.divide: decimal: division by zero`). decimalFault is that prefix,
// once.

// IntToDecimal is `Int.to_decimal`: exact, scale 0, total.
func IntToDecimal(n Int) Decimal {
	return Decimal{Mantissa: n.Big(), Scale: 0}
}

// StringToDecimal is `String.to_decimal`, and None matters here: it
// parses plain base-10 only, so "1e5" is None rather than 100000.
func StringToDecimal(s string) Maybe[Decimal] {
	d, err := ParseDecimal(s)
	if err != nil {
		return None[Decimal]()
	}
	return Some(d)
}

// DecimalToInt is `Decimal.to_int`: the whole part, truncated toward zero.
// Exact and total: an Int holds a whole number of any size.
func DecimalToInt(d Decimal) Int {
	trunc, _ := d.Round(0, RoundDown)
	if trunc.Mantissa == nil {
		return SmallInt(0)
	}
	return BigIntOf(trunc.Mantissa)
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

// FloatToDecimal is `Float.to_decimal` at an explicit scale and mode.
//
// NaN and ±Inf are None. Everything else goes through the float's exact binary
// value as a rational, which is the only way the answer is honest: 0.1 as a
// float64 is 0.1000000000000000055511151231257827, and rounding that at scale 2
// with HalfEven is what yields 0.10 rather than something that happens to look
// right. A `%g` round-trip would agree here and disagree somewhere unlisted.
//
// Traps only for RoundUnnecessary on an inexact value, which is that mode's
// entire contract.
func FloatToDecimal(f float64, scale int64, mode RoundingMode) Maybe[Decimal] {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return None[Decimal]()
	}
	rat := new(big.Rat).SetFloat64(f)
	if rat == nil {
		return None[Decimal]()
	}
	rounded, err := RatToDecimal(rat).Round(int32(scale), mode)
	if err != nil {
		decimalFault("Float.to_decimal", err)
	}
	return Some(rounded)
}

// DecimalDivide is `Decimal.divide`: the explicit-rounding division. Traps on
// a zero divisor and on RoundUnnecessary when the exact quotient does not fit
// at scale.
func DecimalDivide(a, b Decimal, scale int64, mode RoundingMode) Decimal {
	res, err := a.DivRound(b, int32(scale), mode)
	if err != nil {
		decimalFault("Decimal.divide", err)
	}
	return res
}

// DecimalDivideExact is `Decimal.divide_exact`: the exact quotient at its
// preferred scale (DivExact), or None when it does not terminate in base 10.
// A zero divisor traps with Int's `/` text, which the VM prefixes with the
// call's line.
func DecimalDivideExact(a, b Decimal) Maybe[Decimal] {
	if b.IsZero() {
		Trap(divByZeroMessage)
	}
	q, exact := a.DivExact(b)
	if !exact {
		return None[Decimal]()
	}
	return Some(q)
}

// DecimalRound is `Decimal.round`: quantize to exactly scale digits.
//
// Increasing the scale pads and is always exact, which is why RoundUnnecessary
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

// DecimalPow is `Decimal.pow`: d multiplied by itself exp times, exactly,
// at scale exp times d's (1d at scale 0 for exp 0). A negative exponent
// traps, since its answer is a quotient that may not terminate and that
// `Decimal.divide` rounds by a named mode; so does a scale past Decimal's.
func DecimalPow(d Decimal, exp int64) Decimal {
	if exp < 0 {
		Trap(fmt.Sprintf("Decimal.pow: negative exponent %d: divide 1d by the power with Decimal.divide, which names a scale and a rounding mode", exp))
	}
	// Past 2^31 the scale overflows unless it is 0, and the digits run past
	// what memory holds unless the mantissa is 0 or ±1.
	huge := exp > math.MaxInt32
	scale := int64(d.Scale) * min(exp, math.MaxInt32+1)
	if (huge && (d.Scale != 0 || d.MantissaOrZero().CmpAbs(bigOne) > 0)) || scale > math.MaxInt32 || scale < math.MinInt32 {
		Trap(fmt.Sprintf("Decimal.pow: the power of %s to %d has more digits than a Decimal holds", d.Display(), exp))
	}
	m := new(big.Int).Exp(d.MantissaOrZero(), big.NewInt(exp), nil)
	return Decimal{Mantissa: m, Scale: int32(scale)}
}

// DecimalSqrt is `Decimal.sqrt`: the square root of d to exactly scale
// digits after the point, rounded by mode as `Decimal.divide` rounds its
// quotient. A negative d traps, and so does RoundUnnecessary when the root
// does not end within scale digits.
//
// With d = m / 10^k, the root at scale s is sqrt(m * 10^(2s - k)) units of
// 10^-s, so the answer is the integer square root q of A / B, where A and B
// carry the power of ten on whichever side it is positive. The root is
// exact when q*q*B == A, and above the half when 4A > (2q+1)^2 * B.
func DecimalSqrt(d Decimal, scale int64, mode RoundingMode) Decimal {
	if d.Sign() < 0 {
		Trap(fmt.Sprintf("Decimal.sqrt: negative number %s", d.Display()))
	}
	s := int32(scale)
	a := new(big.Int).Set(d.MantissaOrZero())
	b := big.NewInt(1)
	if t := 2*int64(s) - int64(d.Scale); t >= 0 {
		a.Mul(a, pow10(int32(t)))
	} else {
		b = pow10(int32(-t))
	}
	q := new(big.Int).Sqrt(new(big.Int).Quo(a, b))
	// r is how far A is past q's square, in B units: 4(A - q^2 B), against
	// (4q + 1) B, the distance from q^2 to (q + 1/2)^2. applyRounding
	// compares twice its remainder with its divisor, so the divisor is
	// twice that distance.
	sq := new(big.Int).Mul(q, q)
	r := new(big.Int).Sub(a, sq.Mul(sq, b))
	if r.Sign() == 0 {
		return Decimal{Mantissa: q, Scale: s}
	}
	if mode == RoundUnnecessary {
		Trap(fmt.Sprintf("Decimal.sqrt: the square root of %s is not exact at scale %d but mode is Unnecessary", d.Display(), scale))
	}
	r.Lsh(r, 2)
	half := new(big.Int).Lsh(q, 2)
	half.Add(half, bigOne)
	half.Mul(half, b)
	half.Lsh(half, 1)
	return Decimal{Mantissa: applyRounding(q, r, half, 1, mode), Scale: s}
}

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
// The suffix is deliberate and must not be shared with Display. It is what
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
