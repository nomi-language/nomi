package rt

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Decimal is an arbitrary-precision exact base-10 number:
//
//	value = Mantissa × 10^(-Scale)
//
// Scale is the number of fractional digits and is display-significant
// (1.50d has Scale 2 and prints "1.50"); equality/hash/compare are
// value-based (scale-insensitive: 1.50d == 1.5d). Immutable: methods
// never mutate Mantissa in place — they allocate fresh *big.Int values.
//
// This is the third blessed numeric primitive alongside Int and Float.
// Arithmetic is exact-by-default: Add/Sub/Mul never round (scale grows as
// needed); only DivRound/Round round, and they always name a RoundingMode --
// which is rt's own `std/decimal.RoundingMode`, in roundingmode.go. There is
// exactly one encoding of the eight modes in this process, and it is the one
// the Nomi enum lowers to.
type Decimal struct {
	Mantissa *big.Int
	Scale    int32
}

// Small big.Int constants reused read-only across the package. Never mutate
// these in place — any code path that needs a mutable copy must
// new(big.Int).Set(...) first. bigTen is the base used for every scale shift.
var (
	bigOne  = big.NewInt(1)
	bigTwo  = big.NewInt(2)
	bigFive = big.NewInt(5)
	bigTen  = big.NewInt(10)
)

// pow10 returns a fresh 10^n as a *big.Int (n >= 0).
func pow10(n int32) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// stripFactor divides every factor of the prime p out of n, returning a freshly
// allocated quotient and the count of factors removed. n is not mutated. Used by
// DivExact both to test base-10 termination (strip 2s and 5s; a leftover > 1
// means non-terminating) and to compute the minimal terminating scale (v2/v5 of
// the denominator).
func stripFactor(n, p *big.Int) (stripped *big.Int, count int32) {
	stripped = new(big.Int).Set(n)
	q := new(big.Int)
	r := new(big.Int)
	for {
		q.QuoRem(stripped, p, r)
		if r.Sign() != 0 {
			break
		}
		stripped.Set(q)
		count++
	}
	return stripped, count
}

// ParseDecimal parses a plain base-10 numeric literal into a Decimal.
//
// Accepts an optional leading sign, digit groups with `_` separators
// (stripped), and an optional single fractional part:
//
//	"1.50"  "5"  "-0.001"  "1_000.00"  "+5"  "0"
//
// The number of digits after the '.' becomes Scale (so "1.50" has scale 2,
// "5" has scale 0). Exponent notation is not accepted. This is the shared
// parser behind both the `1.50d` literal evaluator and `Decimal.from_string`,
// so it rejects empty/garbage input rather than guessing.
func ParseDecimal(s string) (Decimal, error) {
	orig := s
	if s == "" {
		return Decimal{}, errors.New("decimal: empty string")
	}

	// Optional leading sign.
	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return Decimal{}, fmt.Errorf("decimal: %q has a sign but no digits", orig)
	}

	// Split on the single allowed decimal point.
	intPart := s
	fracPart := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
		if strings.IndexByte(fracPart, '.') >= 0 {
			return Decimal{}, fmt.Errorf("decimal: %q has more than one decimal point", orig)
		}
		// Require digits on both sides of the point ("1." and ".5" are rejected).
		if intPart == "" || fracPart == "" {
			return Decimal{}, fmt.Errorf("decimal: %q must have digits on both sides of '.'", orig)
		}
	}

	intDigits, err := stripUnderscores(intPart)
	if err != nil {
		return Decimal{}, fmt.Errorf("decimal: %q: %w", orig, err)
	}
	fracDigits, err := stripUnderscores(fracPart)
	if err != nil {
		return Decimal{}, fmt.Errorf("decimal: %q: %w", orig, err)
	}

	digits := intDigits + fracDigits
	if digits == "" || !allDigits(digits) {
		return Decimal{}, fmt.Errorf("decimal: %q is not a valid decimal", orig)
	}

	mant, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("decimal: %q is not a valid decimal", orig)
	}
	if neg {
		mant.Neg(mant)
	}
	return Decimal{Mantissa: mant, Scale: int32(len(fracDigits))}, nil
}

// ParseDecimalLexeme parses a Nomi decimal literal — the token as the source
// spells it, `d` suffix and all, and possibly with a leading `-` from a negated
// pattern literal.
//
// One function rather than each caller trimming the suffix. A
// suffix-stripping rule is small enough that two copies look harmless and
// exactly the wrong size to notice diverging.
func ParseDecimalLexeme(lexeme string) (Decimal, error) {
	body := strings.TrimSuffix(strings.TrimSuffix(lexeme, "d"), "D")
	return ParseDecimal(body)
}

// stripUnderscores removes `_` digit separators, rejecting leading/trailing
// or doubled separators (matching Int/Float lexing where `_` sits between
// digits). An all-empty part is allowed (the caller's job to validate).
func stripUnderscores(part string) (string, error) {
	if part == "" {
		return "", nil
	}
	if part[0] == '_' || part[len(part)-1] == '_' {
		return "", errors.New("misplaced '_' separator")
	}
	var b strings.Builder
	prevUnderscore := false
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c == '_' {
			if prevUnderscore {
				return "", errors.New("doubled '_' separator")
			}
			prevUnderscore = true
			continue
		}
		prevUnderscore = false
		b.WriteByte(c)
	}
	return b.String(), nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Display renders the canonical scale-preserving form: the decimal point sits
// Scale digits from the right of the mantissa, with a leading "0" for
// magnitudes < 1, a "-" sign for negatives, and trailing zeros preserved
// (1.50 stays "1.50"). Round-trips with ParseDecimal.
func (v Decimal) Display() string {
	if v.Mantissa == nil {
		// Zero value of the struct — treat as 0 so Display never panics.
		return "0"
	}
	// Work on the magnitude; reattach the sign at the end. Note: a zero
	// mantissa is never negative here, so "-0" never prints.
	neg := v.Mantissa.Sign() < 0
	digits := new(big.Int).Abs(v.Mantissa).String()

	scale := int(v.Scale)
	var body string
	if scale < 0 {
		// Negative scale means value = mantissa × 10^(-scale): an integer with
		// (-scale) trailing zeros. A zero mantissa stays "0" (no padding).
		if v.Mantissa.Sign() == 0 {
			body = "0"
		} else {
			body = digits + strings.Repeat("0", -scale)
		}
	} else if scale == 0 {
		body = digits
	} else if len(digits) > scale {
		// Point falls inside the digit string.
		point := len(digits) - scale
		body = digits[:point] + "." + digits[point:]
	} else {
		// Magnitude < 1: pad with leading zeros after "0.".
		body = "0." + strings.Repeat("0", scale-len(digits)) + digits
	}
	if neg {
		return "-" + body
	}
	return body
}

// Normalize strips trailing fractional zeros: it divides factors of 10 out of
// the mantissa while Scale > 0, so 1.50 → mantissa 15 scale 1 and 100.00 →
// mantissa 100 scale 0. Integer magnitude is never reduced — 100 (scale 0)
// stays mantissa 100 scale 0 (Display "100"). Zero normalizes to mantissa 0,
// scale 0. Used by Hash/Compare to make equality scale-insensitive (Model C).
func (v Decimal) Normalize() Decimal {
	if v.Mantissa == nil || v.Mantissa.Sign() == 0 {
		return Decimal{Mantissa: big.NewInt(0), Scale: 0}
	}
	mant := new(big.Int).Set(v.Mantissa)
	scale := v.Scale
	q := new(big.Int)
	r := new(big.Int)
	for scale > 0 {
		q.QuoRem(mant, bigTen, r)
		if r.Sign() != 0 {
			break // a non-zero last fractional digit — stop
		}
		mant.Set(q)
		scale--
	}
	return Decimal{Mantissa: mant, Scale: scale}
}

// alignScales rescales a and b to a common scale of max(a.Scale, b.Scale),
// returning the two adjusted (freshly-allocated) mantissas and that scale.
func alignScales(a, b Decimal) (am, bm *big.Int, scale int32) {
	am = new(big.Int).Set(a.MantissaOrZero())
	bm = new(big.Int).Set(b.MantissaOrZero())
	switch {
	case a.Scale < b.Scale:
		am.Mul(am, pow10(b.Scale-a.Scale))
		scale = b.Scale
	case b.Scale < a.Scale:
		bm.Mul(bm, pow10(a.Scale-b.Scale))
		scale = a.Scale
	default:
		scale = a.Scale
	}
	return am, bm, scale
}

// MantissaOrZero returns the mantissa, treating a nil mantissa as 0 so the
// struct's zero value behaves like 0.
func (v Decimal) MantissaOrZero() *big.Int {
	if v.Mantissa == nil {
		return big.NewInt(0)
	}
	return v.Mantissa
}

// Add returns a + b exactly. Result scale = max(a.Scale, b.Scale).
func (v Decimal) Add(b Decimal) Decimal {
	am, bm, scale := alignScales(v, b)
	return Decimal{Mantissa: am.Add(am, bm), Scale: scale}
}

// Sub returns a - b exactly. Result scale = max(a.Scale, b.Scale).
func (v Decimal) Sub(b Decimal) Decimal {
	am, bm, scale := alignScales(v, b)
	return Decimal{Mantissa: am.Sub(am, bm), Scale: scale}
}

// Mul returns a * b exactly. Result scale = a.Scale + b.Scale.
func (v Decimal) Mul(b Decimal) Decimal {
	m := new(big.Int).Mul(v.MantissaOrZero(), b.MantissaOrZero())
	return Decimal{Mantissa: m, Scale: v.Scale + b.Scale}
}

// IsZero reports whether the value is exactly zero (any scale).
func (v Decimal) IsZero() bool {
	return v.Mantissa == nil || v.Mantissa.Sign() == 0
}

// Sign returns -1, 0, or +1 by mathematical value.
func (v Decimal) Sign() int {
	return v.MantissaOrZero().Sign()
}

// Abs returns |v| (scale preserved).
func (v Decimal) Abs() Decimal {
	return Decimal{Mantissa: new(big.Int).Abs(v.MantissaOrZero()), Scale: v.Scale}
}

// Negate returns -v (scale preserved). Negating zero stays zero.
func (v Decimal) Negate() Decimal {
	return Decimal{Mantissa: new(big.Int).Neg(v.MantissaOrZero()), Scale: v.Scale}
}

// Compare returns -1, 0, or +1 comparing a and b by mathematical value,
// scale-insensitively (1.50d Compare 1.5d == 0).
func (v Decimal) Compare(b Decimal) int {
	am, bm, _ := alignScales(v, b)
	return am.Cmp(bm)
}

// DivExact returns a / b exactly, or ok=false when the quotient is
// non-terminating in base 10 (after reducing a/b to lowest terms, the
// denominator has a prime factor other than 2 or 5). The result's preferred
// scale is a.Scale - b.Scale, expanded as far as needed for exactness (and
// never below the preferred scale). Callers must check IsZero on b first —
// DivExact on a zero divisor returns ok=false (no panic), but the operator
// path should raise a distinct division-by-zero error.
//
// Backs the exact-or-trap `/` operator.
func (v Decimal) DivExact(b Decimal) (Decimal, bool) {
	if b.IsZero() {
		return Decimal{}, false
	}
	if v.IsZero() {
		// 0 / b is exactly 0 at the preferred scale (clamped to >= 0).
		ps := v.Scale - b.Scale
		if ps < 0 {
			ps = 0
		}
		return Decimal{Mantissa: big.NewInt(0), Scale: ps}, true
	}

	// The exact value is (v.Mantissa / b.Mantissa) × 10^(b.Scale - v.Scale).
	// Reduce num/den to lowest terms, then the quotient terminates iff the
	// reduced denominator is of the form 2^i · 5^j.
	num := new(big.Int).Set(v.MantissaOrZero())
	den := new(big.Int).Set(b.MantissaOrZero())
	// Fold the 10^(b.Scale - v.Scale) factor into num/den as powers of 10.
	if d := b.Scale - v.Scale; d > 0 {
		num.Mul(num, pow10(d))
	} else if d < 0 {
		den.Mul(den, pow10(-d))
	}
	// Normalize signs onto num; den positive.
	if den.Sign() < 0 {
		num.Neg(num)
		den.Neg(den)
	}

	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(num), den)
	num.Quo(num, g)
	den.Quo(den, g)

	// Strip 2s and 5s from den; whatever remains decides termination. The
	// 2-then-5 counts also give v2(den)/v5(den), the minimal terminating scale.
	afterTwos, i := stripFactor(den, bigTwo)
	leftover, j := stripFactor(afterTwos, bigFive)
	if leftover.Cmp(bigOne) != 0 {
		return Decimal{}, false // non-terminating
	}

	// Terminating. The minimal scale k makes den a power of 10: k = max(i, j) is
	// the smallest exponent for which 10^k is a multiple of den = 2^i·5^j.
	k := i
	if j > k {
		k = j
	}
	// To turn den = 2^i·5^j into 10^k, multiply num (and den) by 2^(k-i)·5^(k-j):
	// the denominator becomes 2^k·5^k = 10^k and value = resMant × 10^-k.
	resMant := new(big.Int).Set(num)
	if k-i > 0 {
		resMant.Mul(resMant, new(big.Int).Exp(bigTwo, big.NewInt(int64(k-i)), nil))
	}
	if k-j > 0 {
		resMant.Mul(resMant, new(big.Int).Exp(bigFive, big.NewInt(int64(k-j)), nil))
	}
	// resMant is now num·(...)/den exactly, and the value = resMant × 10^-k.
	res := Decimal{Mantissa: resMant, Scale: k}

	// Honour the preferred scale: pad with trailing zeros if the minimal
	// scale is below it (Java's preferred-scale rule). Never round.
	preferred := v.Scale - b.Scale
	if preferred > res.Scale {
		res = res.rescaleUp(preferred)
	}
	return res, true
}

// rescaleUp returns v at a strictly larger scale by padding trailing zeros
// (exact). Caller guarantees target >= v.Scale.
func (v Decimal) rescaleUp(target int32) Decimal {
	if target == v.Scale {
		return v
	}
	m := new(big.Int).Mul(v.MantissaOrZero(), pow10(target-v.Scale))
	return Decimal{Mantissa: m, Scale: target}
}

// Round quantizes v to exactly the given scale using mode. Increasing the
// scale only pads trailing zeros (always exact). Decreasing the scale divides
// out the surplus digits and applies mode to the discarded remainder. A
// negative target scale is legitimate (rounding to tens/hundreds/…, like Java
// BigDecimal): scale -2 rounds to the nearest hundred and Displays e.g. "1200".
// Returns an error only for RoundUnnecessary when rounding would lose
// information; every other mode is total.
func (v Decimal) Round(scale int32, mode RoundingMode) (Decimal, error) {
	if scale >= v.Scale {
		return v.rescaleUp(scale), nil
	}
	// Drop (v.Scale - scale) least-significant digits with rounding.
	drop := v.Scale - scale
	divisor := pow10(drop)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(v.MantissaOrZero(), divisor, r) // truncated toward zero; r has sign of dividend
	if r.Sign() == 0 {
		return Decimal{Mantissa: q, Scale: scale}, nil // exact
	}
	if mode == RoundUnnecessary {
		return Decimal{}, fmt.Errorf("decimal: rounding necessary to reach scale %d but mode is Unnecessary", scale)
	}
	q = applyRounding(q, r, divisor, v.Sign(), mode)
	return Decimal{Mantissa: q, Scale: scale}, nil
}

// DivRound divides a / b to exactly the requested scale with the given mode.
// Returns an error if b is zero (the caller's division-by-zero), or for
// RoundUnnecessary when the exact quotient does not fit at scale. Backs
// Decimal.divide.
func (v Decimal) DivRound(b Decimal, scale int32, mode RoundingMode) (Decimal, error) {
	if b.IsZero() {
		return Decimal{}, errors.New("decimal: division by zero")
	}
	// Compute the quotient scaled to `scale` fractional digits in one division.
	// value(a)/value(b) at scale s = round( a.Mantissa·10^(s - a.Scale + b.Scale)
	// / b.Mantissa ). Build numerator/denominator as integers, then divide with
	// rounding on the remainder.
	num := new(big.Int).Set(v.MantissaOrZero())
	den := new(big.Int).Set(b.MantissaOrZero())
	shift := scale - v.Scale + b.Scale
	if shift > 0 {
		num.Mul(num, pow10(shift))
	} else if shift < 0 {
		den.Mul(den, pow10(-shift))
	}
	// Sign of the true quotient (before we make the divisor positive).
	resultSign := num.Sign() * den.Sign()
	if den.Sign() < 0 {
		num.Neg(num)
		den.Neg(den)
	}
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(num, den, r) // q,r truncated toward zero; with num,den >=0 here, r >= 0
	if r.Sign() == 0 {
		return Decimal{Mantissa: q, Scale: scale}, nil // exact
	}
	if mode == RoundUnnecessary {
		return Decimal{}, fmt.Errorf("decimal: division of %s by %s is not exact at scale %d but mode is Unnecessary", v.Display(), b.Display(), scale)
	}
	q = applyRounding(q, r, den, resultSign, mode)
	return Decimal{Mantissa: q, Scale: scale}, nil
}

// applyRounding adjusts a truncated-toward-zero quotient q (with remainder r
// and divisor d, both representing the magnitude split — see callers) by the
// rounding mode. sign is the sign of the exact true value (-1/0/+1); it
// determines the direction "away from zero" points and resolves Ceiling/Floor.
//
// q and r come straight from big.Int.QuoRem, which truncates toward zero, so
// |q| is the magnitude already kept and r carries q's sign. The decision is
// made on the magnitudes: compare 2·|r| to d.
func applyRounding(q, r, d *big.Int, sign int, mode RoundingMode) *big.Int {
	out := new(big.Int).Set(q)
	absR := new(big.Int).Abs(r)
	twiceR := new(big.Int).Lsh(absR, 1) // 2·|r|
	cmp := twiceR.Cmp(d)                // <0 below half, ==0 exactly half, >0 above half

	roundAway := false
	switch mode {
	case RoundUp:
		roundAway = true
	case RoundDown:
		roundAway = false
	case RoundCeiling:
		roundAway = sign > 0 // toward +∞: away from zero only for positives
	case RoundFloor:
		roundAway = sign < 0 // toward −∞: away from zero only for negatives
	case RoundHalfUp:
		roundAway = cmp >= 0
	case RoundHalfDown:
		roundAway = cmp > 0
	case RoundHalfEven:
		if cmp > 0 {
			roundAway = true
		} else if cmp < 0 {
			roundAway = false
		} else {
			// Exactly half: round to even — away iff the kept digit is odd.
			roundAway = out.Bit(0) == 1
		}
	}
	if roundAway {
		if sign >= 0 {
			out.Add(out, bigOne)
		} else {
			out.Sub(out, bigOne)
		}
	}
	return out
}
