package rt

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strings"
	"unicode"
)

// The Go side of `std`'s host externs — the 45% of the standard library that is
// declared in Nomi and defined in Go.
//
// # Why these live here
//
// A `host fn` in `std/*.nomi` has no Nomi body, so there is nothing to lower:
// the VM crosses into Go for it, through internal/stdlibbindings' RtFuncs
// table. rt links no front end (enforced by
// TestRuntimeArtifactLinksNoFrontEnd and
// TestRuntimeImportsOnlyItsAllowlist), so anything that runs a program
// can reach these. One implementation per function: a fixture covers the
// inputs it names, and `String.contains` has as many as there are pairs of
// strings.
//
// # The trap texts are fixed, not invented
//
// Some of these fault, and the text is Nomi-observable: `nomi run` prints the
// fault after the line of the program's call, so each is a recorded text.
// Do not tidy one: the golden files hold these exact strings.

// --- std/strings -----------------------------------------------------------
//
// Byte-based operations only. The grapheme-based half of std/strings —
// `length`, `slice`, `reverse`, `normalize`, `to_codepoints` — is deliberately
// absent from this section: grapheme work lives in grapheme.go over
// github.com/rivo/uniseg, and normalization needs golang.org/x/text, which rt
// does not require. None of them is half-implemented here over code points.

// The `impl Matcher for String` functions take the matcher (the needle)
// first and the text second, the order std/matcher declares; `String.split`
// and the other search functions call them through a `where M: Matcher`
// bound.

// StringContainedIn is String's `Matcher.is_contained_in`: byte substring
// containment.
func StringContainedIn(needle, s string) bool { return strings.Contains(s, needle) }

// StringPrefixOf is String's `Matcher.is_prefix_of`.
func StringPrefixOf(prefix, s string) bool { return strings.HasPrefix(s, prefix) }

// StringSuffixOf is String's `Matcher.is_suffix_of`.
func StringSuffixOf(suffix, s string) bool { return strings.HasSuffix(s, suffix) }

// StringFindAllIn is String's `Matcher.find_all_in`: one copy of needle per
// non-overlapping occurrence, which is what a regex matching exactly needle
// answers. An empty needle occurs at every codepoint boundary and at both
// ends, strings.Count's rule and the empty regex's.
func StringFindAllIn(needle, s string) *List[string] {
	var out *List[string]
	for range strings.Count(s, needle) {
		out = Cons(needle, out)
	}
	return out
}

// StringUppercase is `String.uppercase`: Unicode-aware, locale-independent.
func StringUppercase(s string) string { return strings.ToUpper(s) }

// StringLowercase is `String.lowercase`.
func StringLowercase(s string) string { return strings.ToLower(s) }

// StringIsUpper is `String.is_upper`, Python's str.isupper: s has a cased
// character, and every cased character is upper case. A cased character is
// one unicode.IsUpper, IsLower or IsTitle accepts, so a title-case letter
// such as `ǅ` makes s neither upper nor lower case.
func StringIsUpper(s string) bool { return stringInCase(s, unicode.IsUpper) }

// StringIsLower is `String.is_lower`, the mirror of StringIsUpper.
func StringIsLower(s string) bool { return stringInCase(s, unicode.IsLower) }

func stringInCase(s string, in func(rune) bool) bool {
	cased := false
	for _, r := range s {
		switch {
		case in(r):
			cased = true
		case unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r):
			return false
		}
	}
	return cased
}

// StringReplaceIn is String's `Matcher.replace_in`: every non-overlapping
// occurrence.
func StringReplaceIn(old, s, new string) string { return strings.ReplaceAll(s, old, new) }

// StringTrim is `String.trim`: Go's TrimSpace rules, which std/strings documents
// as the definition rather than as an implementation detail.
func StringTrim(s string) string { return strings.TrimSpace(s) }

// StringSplitIn is String's `Matcher.split_in`: byte-substring split, every occurrence, the
// separator consumed. An empty separator splits at every UTF-8 codepoint
// boundary, which is Go's own rule and is what std/strings documents as the
// definition ("An empty `separator` splits at every UTF-8 codepoint boundary").
//
// The first rt function over a `*List[T]`, and the direction matters: the result
// is built back-to-front with Cons because Cons is the O(1) operation on a
// persistent cons list (see list.go), so this is one pass and n allocations
// rather than n prepends onto a growing head.
func StringSplitIn(separator, s string) *List[string] {
	return listOf(strings.Split(s, separator))
}

// StringWords is `String.words`: Go's strings.Fields, which splits on runs of
// Unicode White_Space (unicode.IsSpace, so U+00A0 and U+3000 count) and never
// yields an empty string.
func StringWords(s string) *List[string] {
	return listOf(strings.Fields(s))
}

// StringLines is `String.lines`: split on "\n", with a "\r" just before a "\n"
// dropped too. A lone "\r" is not a terminator, and a final terminator does not
// start an empty last line, so "" has no lines and "\n" has one empty line.
func StringLines(s string) *List[string] {
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		if i < len(parts)-1 || strings.HasSuffix(s, "\n") {
			parts[i] = strings.TrimSuffix(p, "\r")
		}
	}
	return listOf(parts)
}

// maxStringBytes bounds the String that String.repeat builds, as maxShift
// bounds an Int: a larger one does not fit in memory a program can have, so
// a request for one faults with a reason rather than running out of memory,
// which ends the process with Go's own report.
const maxStringBytes = maxShift / 8

// StringRepeat is `String.repeat`. std declares count a NonNegativeInt of
// any size: the empty string repeats to itself however many times, and
// copies past maxStringBytes trap naming the count.
func StringRepeat(s string, count Int) string {
	if s == "" {
		return ""
	}
	n, ok := count.Int64()
	if !ok || n > maxStringBytes/int64(len(s)) {
		Trap(fmt.Sprintf("String.repeat: %s copies of a %d-byte string are more than memory holds", count, len(s)))
	}
	return strings.Repeat(s, int(n))
}

// StringCompare is std/strings' private `string_compare`: byte-lexicographic,
// -1/0/1, backing the String `Comparable` impl.
func StringCompare(a, b string) int64 {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// StringHash is `Hashable.hash` for String: SipHash-1-3 over the UTF-8
// bytes under the process's key (hash.go, "Keyed by the process"),
// reinterpreted as int64 so the full bit pattern survives. The same within
// one process, and different between processes unless NOMI_HASH_SEED fixes
// the key.
func StringHash(s string) int64 { return int64(sipHash13(hashKey.sip0, hashKey.sip1, s)) }

// --- std/int ---------------------------------------------------------------

// IntToString is `Int.to_string`: base 10, at any size.
func IntToString(n Int) string { return n.String() }

// IntToFloat is `Int.to_float`. Total: the Float nearest n, ties to even,
// and an infinity past Float's range. Precision loss is possible above 2^53.
func IntToFloat(n Int) float64 {
	if x, ok := n.Int64(); ok {
		return float64(x)
	}
	f, _ := new(big.Float).SetInt(n.Big()).Float64()
	return f
}

// IntRatioToFloat is std/averageable's `int_ratio_to_float`, behind Int's
// `Averageable` impl: the Float nearest the exact fraction num / den, ties
// to even, an infinity past Float's range. den is a PositiveInt; a
// denominator that is not positive traps. When both fit in 53 bits each
// converts exactly and one IEEE division rounds the quotient once;
// otherwise big.Rat rounds the exact fraction.
func IntRatioToFloat(num, den Int) float64 {
	if den.Sign() <= 0 {
		Trap(fmt.Sprintf("int_ratio_to_float: denominator %s is not positive", den))
	}
	const exact = 1 << 53
	if n, ok := num.Int64(); ok && n > -exact && n < exact {
		if d, ok := den.Int64(); ok && d < exact {
			return float64(n) / float64(d)
		}
	}
	f, _ := new(big.Rat).SetFrac(num.Big(), den.Big()).Float64()
	return f
}

// IntWrapAdd is `Int.wrapping_add`: a + b modulo 2^64, as an int64.
func IntWrapAdd(a, b Int) int64 { return int64(a.Low64() + b.Low64()) }

// IntWrapSub is `Int.wrapping_sub`: a - b modulo 2^64, as an int64.
func IntWrapSub(a, b Int) int64 { return int64(a.Low64() - b.Low64()) }

// IntWrapMul is `Int.wrapping_mul`: a * b modulo 2^64, as an int64.
func IntWrapMul(a, b Int) int64 { return int64(a.Low64() * b.Low64()) }

// IntBitCount is `Int.bit_count`: the number of 1 bits in n. n is a
// NonNegativeInt in Nomi; a negative one, which only rt's own callers could
// pass, faults, since it has endless 1 bits.
func IntBitCount(n Int) int64 {
	if n.Sign() < 0 {
		Trap(fmt.Sprintf("Int.bit_count: %s is negative, and a negative Int has endless 1 bits", n))
	}
	if x, ok := n.Int64(); ok {
		return int64(bits.OnesCount64(uint64(x)))
	}
	count := 0
	for _, w := range n.Big().Bits() {
		count += bits.OnesCount(uint(w))
	}
	return int64(count)
}

// IntBitLength is `Int.bit_length`: the bits n needs besides a sign, a
// non-negative n's math/big BitLen and a negative n's ~n's.
func IntBitLength(n Int) int64 {
	v := n.Value()
	if n.Sign() < 0 {
		v = IntNot(v)
	}
	if x, ok := v.(int64); ok {
		return int64(bits.Len64(uint64(x)))
	}
	return int64(IntBig(v).BitLen())
}

// maxShift bounds Int.shift_left's count and Int.pow's result: an Int of more
// bits than this does not fit in memory a program can have, so a request for
// one faults with a reason rather than running out of memory.
const maxShift int64 = 1 << 34

// IntPow is `Int.pow`: n multiplied by itself exp times, exact. exp is a
// NonNegativeInt in Nomi; a negative one, which only rt's own callers could
// pass, faults, since its answer is a fraction.
func IntPow(n, exp Int) Int {
	if exp.Sign() < 0 {
		Trap(fmt.Sprintf("Int.pow: negative exponent %s: the power is a fraction, which Float.pow gives", exp))
	}
	if x, ok := n.Int64(); ok {
		switch x {
		case 0, 1:
			if exp.Sign() == 0 {
				return SmallInt(1)
			}
			return n
		case -1:
			if e, ok := exp.Int64(); ok && e%2 == 0 {
				return SmallInt(1)
			} else if !ok && exp.Big().Bit(0) == 0 {
				return SmallInt(1)
			}
			return n
		}
		if e, ok := exp.Int64(); ok {
			if p, ok := powSmall(x, e); ok {
				return SmallInt(p)
			}
		}
	}
	e, fits := exp.Int64()
	if !fits || e > maxShift/int64(n.Big().BitLen()-1) {
		Trap(fmt.Sprintf("Int.pow: exponent %s is too large: the power would have more than %d bits", exp, maxShift))
	}
	return IntOf(intOwned(new(big.Int).Exp(n.Big(), big.NewInt(e), nil)))
}

// powSmall is n^exp by squaring when it fits int64 and is not BigWord's
// value; ok is false otherwise.
func powSmall(n, exp int64) (int64, bool) {
	result, base, e := int64(1), n, exp
	for e > 0 {
		if e&1 == 1 {
			if MulOverflows(result, base) {
				return 0, false
			}
			result *= base
		}
		e >>= 1
		if e > 0 {
			if MulOverflows(base, base) {
				return 0, false
			}
			base *= base
		}
	}
	return result, uint64(result) != BigWord
}

// IntSqrt is `Int.sqrt`: the greatest r with r*r <= n. n is a NonNegativeInt
// in Nomi; a negative one faults.
// The float square root is within one of the answer for an int64, and the
// corrections compare in uint64, where the square of every candidate fits.
func IntSqrt(n Int) Int {
	if n.Sign() < 0 {
		Trap(fmt.Sprintf("Int.sqrt: negative number %s", n))
	}
	if x, ok := n.Int64(); ok {
		r, u := uint64(math.Sqrt(float64(x))), uint64(x)
		for r*r > u {
			r--
		}
		for (r+1)*(r+1) <= u {
			r++
		}
		return SmallInt(int64(r))
	}
	return IntOf(intOwned(new(big.Int).Sqrt(n.Big())))
}

// IntAsCount is std/iter's `as_count`: a count `Iter.count` or
// `Iter.count_by` made, which is never negative, passed through as the
// NonNegativeInt the Nomi side declares it to be without checking the rule.
func IntAsCount(n Int) Int {
	return n
}

// IntGcd is `Int.gcd`: the greatest common divisor of |a| and |b|, never
// negative, and 0 for gcd(0, 0).
func IntGcd(a, b Int) Int {
	x, y := new(big.Int).Abs(a.Big()), new(big.Int).Abs(b.Big())
	return IntOf(intOwned(new(big.Int).GCD(nil, nil, x, y)))
}

// IntLcm is `Int.lcm`: the least common multiple of |a| and |b|, never
// negative, and 0 when either is 0.
func IntLcm(a, b Int) Int {
	if a.Sign() == 0 || b.Sign() == 0 {
		return SmallInt(0)
	}
	x, y := new(big.Int).Abs(a.Big()), new(big.Int).Abs(b.Big())
	g := new(big.Int).GCD(nil, nil, x, y)
	return IntOf(intOwned(x.Mul(x.Quo(x, g), y)))
}

// IntToDigits is `Int.to_digits`: n's digits in base, most significant
// first, and [0] for 0. n is a NonNegativeInt and base a Radix in Nomi; a
// negative n or a base below 2 faults.
func IntToDigits(n, base Int) *List[Int] {
	if IntCompare(base.Value(), int64(2)) < 0 {
		Trap(fmt.Sprintf("Int.to_digits: base %s is less than 2", base))
	}
	if n.Sign() < 0 {
		Trap(fmt.Sprintf("Int.to_digits: negative number %s", n))
	}
	if n.Sign() == 0 {
		return Cons(SmallInt(0), nil)
	}
	if x, ok := n.Int64(); ok {
		if b, ok := base.Int64(); ok {
			var out *List[Int]
			for x > 0 {
				out = Cons(SmallInt(x%b), out)
				x /= b
			}
			return out
		}
	}
	var out *List[Int]
	x, b, d := n.Big(), base.Big(), new(big.Int)
	for x.Sign() > 0 {
		x.QuoRem(x, b, d)
		out = Cons(BigIntOf(d), out)
	}
	return out
}

// IntFromDigits is `Int.from_digits`: the Int whose digits in base, most
// significant first, are digits; Some(0) for none. None when a digit is
// outside 0 to base - 1. base is a Radix in Nomi; one below 2 faults.
func IntFromDigits(digits *List[Int], base Int) Option[Int] {
	if IntCompare(base.Value(), int64(2)) < 0 {
		Trap(fmt.Sprintf("Int.from_digits: base %s is less than 2", base))
	}
	n := Value(int64(0))
	for cur := digits; cur != nil; cur = cur.Tail {
		d := cur.Head.Value()
		if IntSign(d) < 0 || IntCompare(d, base.Value()) >= 0 {
			return None[Int]()
		}
		n = IntAdd(IntMul(n, base.Value()), d)
	}
	return Some(IntOf(n))
}

// --- std/float -------------------------------------------------------------

// FloatNaN is `Float.nan`.
func FloatNaN() float64 { return math.NaN() }

// FloatPositiveInfinity is `Float.positive_infinity`.
func FloatPositiveInfinity() float64 { return math.Inf(1) }

// FloatNegativeInfinity is `Float.negative_infinity`.
func FloatNegativeInfinity() float64 { return math.Inf(-1) }

// FloatIsNaN is `Float.is_nan`.
func FloatIsNaN(x float64) bool { return math.IsNaN(x) }

// FloatRound is `Float.round`, and it is round-half-to-even, not Go's
// math.Round. `Float.round(2.5)` is 2.0 in Nomi and 3.0 under math.Round, so
// this is the one function in this file where the obvious Go spelling is the
// wrong answer.
//
// With a scale or another mode it rounds x's exact binary value to that many
// digits after the decimal point (a negative scale rounds to tens, hundreds,
// ...) as Decimal.round rounds, and answers the Float nearest the result.
// The binary value is what rounds: 2.675 is
// 2.67499999999999982236431605997495353221893310546875, so it is 2.67 at
// scale 2 under every half mode. NaN and the infinities pass through, and a
// zero result keeps x's sign.
//
// scale is an Int of any size: past int64 it saturates, which the bounds
// below already hold to.
func FloatRound(x float64, scaleArg Int, mode RoundingMode) float64 {
	scale := scaleArg.Saturated()
	if scale == 0 && mode == RoundHalfEven {
		return math.RoundToEven(x)
	}
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	// A finite Float has at most 1074 binary digits after the point, so at
	// most 1074 decimal ones: a scale at or above 1100 keeps every digit.
	// 10^400 is past the largest Float, so every scale below -400 rounds as
	// -400 does; holding it there keeps it within Decimal's int32 scale.
	if scale >= 1100 {
		return x
	}
	if scale < -400 {
		scale = -400
	}
	rounded, err := RatToDecimal(new(big.Rat).SetFloat64(x)).Round(int32(scale), mode)
	if err != nil {
		// The scale is held to -400..1100 above and a finite Float's own
		// scale is under 1100, so no drop is past a Decimal's.
		panic("unreachable: " + err.Error())
	}
	out := DecimalToFloat(rounded)
	if out == 0 {
		return math.Copysign(0, x)
	}
	return out
}

// FloatFloor is `Float.floor`.
func FloatFloor(x float64) float64 { return math.Floor(x) }

// FloatCeil is `Float.ceil`.
func FloatCeil(x float64) float64 { return math.Ceil(x) }

// FloatTrunc is `Float.trunc`.
func FloatTrunc(x float64) float64 { return math.Trunc(x) }

// FloatPow is `Float.pow`: Go's math.Pow and its IEEE special cases.
func FloatPow(x, y float64) float64 { return math.Pow(x, y) }

// FloatSqrt is `Float.sqrt`: NaN for a negative x, -0.0 for -0.0.
func FloatSqrt(x float64) float64 { return math.Sqrt(x) }

// FloatExp is `Float.exp`: e to the x.
func FloatExp(x float64) float64 { return math.Exp(x) }

// FloatLog is `Float.log`, the natural logarithm.
func FloatLog(x float64) float64 { return math.Log(x) }

// FloatLog2 is `Float.log2`.
func FloatLog2(x float64) float64 { return math.Log2(x) }

// FloatLog10 is `Float.log10`.
func FloatLog10(x float64) float64 { return math.Log10(x) }

// FloatSin is `Float.sin`, of an angle in radians.
func FloatSin(x float64) float64 { return math.Sin(x) }

// FloatCos is `Float.cos`, of an angle in radians.
func FloatCos(x float64) float64 { return math.Cos(x) }

// FloatTan is `Float.tan`, of an angle in radians.
func FloatTan(x float64) float64 { return math.Tan(x) }

// FloatAtan2 is `Float.atan2`: the angle of the point (x, y) from the
// positive x axis, in radians from -π to π.
func FloatAtan2(y, x float64) float64 { return math.Atan2(y, x) }

// FloatBits is `Float.to_bits`: the IEEE 754 bit pattern as an Int64, two's
// complement, so a negative Float's is negative. It backs the Float
// `Hashable` impl too.
func FloatBits(x float64) int64 { return int64(math.Float64bits(x)) }
