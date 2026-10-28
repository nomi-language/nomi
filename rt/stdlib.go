package rt

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strings"
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
// inputs it names, and `String.contains?` has as many as there are pairs of
// strings.
//
// # The trap texts are fixed, not invented
//
// Two of these fault, and the text is Nomi-observable: `nomi run` prints the
// fault verbatim with no `line N:` prefix. So
// `Int.shift_left: negative shift count -1` and
// `strings.String.repeat: negative count -2` are the recorded texts,
// inconsistent qualification included. Do not tidy them: the golden files
// hold these exact strings.

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

// StringContainedIn is String's `Matcher.contained_in?`: byte substring
// containment.
func StringContainedIn(needle, s string) bool { return strings.Contains(s, needle) }

// StringPrefixOf is String's `Matcher.prefix_of?`.
func StringPrefixOf(prefix, s string) bool { return strings.HasPrefix(s, prefix) }

// StringSuffixOf is String's `Matcher.suffix_of?`.
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

// StringToUpper is `String.to_upper`: Unicode-aware, locale-independent.
func StringToUpper(s string) string { return strings.ToUpper(s) }

// StringToLower is `String.to_lower`.
func StringToLower(s string) string { return strings.ToLower(s) }

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

// StringRepeat is `String.repeat`. A negative count is a runtime fault.
func StringRepeat(s string, count int64) string {
	if count < 0 {
		Trap(negativeRepeatText(count))
		return ""
	}
	return strings.Repeat(s, int(count))
}

func negativeRepeatText(count int64) string {
	return fmt.Sprintf("strings.String.repeat: negative count %d", count)
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

// StringHash is `Hashable.hash` for String: FNV-1a 64-bit over the UTF-8 bytes,
// reinterpreted as int64 so the full bit pattern survives. Stable within one
// process; not cryptographic and not stable across runs.
func StringHash(s string) int64 {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	h := offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return int64(h)
}

// --- std/int ---------------------------------------------------------------

// IntToFloat is `Int.to_float`. Total: every int64 maps to a float64, with
// precision loss possible above 2^53.
func IntToFloat(x int64) float64 { return float64(x) }

// IntBitAnd is `Int.bitwise_and`.
func IntBitAnd(a, b int64) int64 { return a & b }

// IntBitOr is `Int.bitwise_or`.
func IntBitOr(a, b int64) int64 { return a | b }

// IntBitXor is `Int.bitwise_xor`.
func IntBitXor(a, b int64) int64 { return a ^ b }

// IntBitNot is `Int.bitwise_not`: one's complement.
func IntBitNot(a int64) int64 { return ^a }

// IntShiftLeft is `Int.shift_left`. A negative count faults; a count >= 64
// shifts every bit out to 0, which is Go's own rule and the one std/int
// documents.
func IntShiftLeft(a, n int64) int64 {
	if n < 0 {
		Trap(negativeShiftText("shift_left", n))
		return 0
	}
	return a << uint64(n)
}

// IntShiftRight is `Int.shift_right`: arithmetic (sign-extending).
func IntShiftRight(a, n int64) int64 {
	if n < 0 {
		Trap(negativeShiftText("shift_right", n))
		return 0
	}
	return a >> uint64(n)
}

func negativeShiftText(op string, n int64) string {
	return fmt.Sprintf("Int.%s: negative shift count %d", op, n)
}

// IntPow is `Int.pow`: n multiplied by itself exp times, by squaring. A
// negative exponent traps, since its answer is a fraction, and a power past
// Int's range traps as `*` does.
func IntPow(n, exp int64) int64 {
	if exp < 0 {
		Trap(fmt.Sprintf("Int.pow: negative exponent %d: the power is a fraction, which Float.pow gives", exp))
		return 0
	}
	result, base, e := int64(1), n, exp
	for e > 0 {
		if e&1 == 1 {
			if MulOverflows(result, base) {
				Trap(intOverflowText("pow", n, exp))
			}
			result *= base
		}
		e >>= 1
		// The base is squared only while a higher bit remains, and every
		// power that needs the square is at least the square in magnitude,
		// so an overflowing square is an overflowing power.
		if e > 0 {
			if MulOverflows(base, base) {
				Trap(intOverflowText("pow", n, exp))
			}
			base *= base
		}
	}
	return result
}

func intOverflowText(fn string, a, b int64) string {
	return fmt.Sprintf("integer overflow: Int.%s(%d, %d)", fn, a, b)
}

// IntSqrt is `Int.sqrt`: the greatest r with r*r <= n. A negative n traps.
// The float square root is within one of the answer, and the corrections
// compare in uint64, where the square of every candidate fits.
func IntSqrt(n int64) int64 {
	if n < 0 {
		Trap(fmt.Sprintf("Int.sqrt: negative number %d", n))
		return 0
	}
	r, u := uint64(math.Sqrt(float64(n))), uint64(n)
	for r*r > u {
		r--
	}
	for (r+1)*(r+1) <= u {
		r++
	}
	return int64(r)
}

// magnitude is |n| as a uint64, which holds |Int.min_value|.
func magnitude(n int64) uint64 {
	if n < 0 {
		return uint64(-(n + 1)) + 1
	}
	return uint64(n)
}

func gcdMagnitude(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// IntGcd is `Int.gcd`: the greatest common divisor of |a| and |b|, never
// negative, and 0 for gcd(0, 0). It traps only when the answer is 2^63, one
// past Int's range: gcd(Int.min_value, 0) and gcd(Int.min_value,
// Int.min_value).
func IntGcd(a, b int64) int64 {
	g := gcdMagnitude(magnitude(a), magnitude(b))
	if g > math.MaxInt64 {
		Trap(intOverflowText("gcd", a, b))
	}
	return int64(g)
}

// IntLcm is `Int.lcm`: the least common multiple of |a| and |b|, never
// negative, and 0 when either is 0. A multiple past Int's range traps.
func IntLcm(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	ua, ub := magnitude(a), magnitude(b)
	hi, lo := bits.Mul64(ua/gcdMagnitude(ua, ub), ub)
	if hi != 0 || lo > math.MaxInt64 {
		Trap(intOverflowText("lcm", a, b))
	}
	return int64(lo)
}

// IntToDigits is `Int.to_digits`: n's digits in base, most significant
// first, and [0] for 0. A base below 2 and a negative n trap.
func IntToDigits(n, base int64) *List[int64] {
	if base < 2 {
		Trap(fmt.Sprintf("Int.to_digits: base %d is less than 2", base))
	}
	if n < 0 {
		Trap(fmt.Sprintf("Int.to_digits: negative number %d", n))
	}
	if n == 0 {
		return Cons[int64](0, nil)
	}
	var out *List[int64]
	for n > 0 {
		out = Cons(n%base, out)
		n /= base
	}
	return out
}

// IntFromDigits is `Int.from_digits`: the Int whose digits in base, most
// significant first, are digits; Some(0) for none. None when a digit is
// outside 0 to base - 1 or the number is past Int's range. A base below 2
// traps.
func IntFromDigits(digits *List[int64], base int64) Maybe[int64] {
	if base < 2 {
		Trap(fmt.Sprintf("Int.from_digits: base %d is less than 2", base))
	}
	var n int64
	for cur := digits; cur != nil; cur = cur.Tail {
		d := cur.Head
		if d < 0 || d >= base || MulOverflows(n, base) || AddOverflows(n*base, d) {
			return None[int64]()
		}
		n = n*base + d
	}
	return Some(n)
}

// --- std/float -------------------------------------------------------------

// FloatNaN is `Float.nan`.
func FloatNaN() float64 { return math.NaN() }

// FloatPositiveInfinity is `Float.positive_infinity`.
func FloatPositiveInfinity() float64 { return math.Inf(1) }

// FloatNegativeInfinity is `Float.negative_infinity`.
func FloatNegativeInfinity() float64 { return math.Inf(-1) }

// FloatIsNaN is `Float.nan?`.
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
// scale 2 under every half mode. NaN and the infinities pass through, a zero
// result keeps x's sign, and RoundUnnecessary traps when rounding would
// change the value.
func FloatRound(x float64, scale int64, mode RoundingMode) float64 {
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
		Trap(fmt.Sprintf("Float.round: rounding necessary to reach scale %d but mode is Unnecessary", scale))
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

// FloatRequireTolerance is std/float's private `require_tolerance`, the check
// `Float.equal_within?` makes of its tolerance before it compares: a negative
// or NaN tolerance is a value the caller chose, so it traps, naming it.
func FloatRequireTolerance(tolerance float64) Unit {
	if math.IsNaN(tolerance) || tolerance < 0 {
		Trap("Float.equal_within?: tolerance must not be negative or NaN, got " + FormatFloat(tolerance))
	}
	return Unit{}
}

// FloatBits is std/float's private `float_bits`: the IEEE-754 bit pattern as an
// Int, backing the Float `Hashable` impl.
func FloatBits(x float64) int64 { return int64(math.Float64bits(x)) }
