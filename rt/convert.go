package rt

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// The stdlib conversions whose result is a `Maybe`, and why they live here.
//
// `Float.to_int` and `String.to_int` are `host fn`s in std/float.nomi and
// std/strings.nomi, and both answer `Maybe<Int>` — a value is either
// representable or it is not, and the type says so rather than a sentinel.
//
// They are in rt so the rule has one encoding. An agreement fixture between
// two callers of one implementation is vacuous, and an agreement fixture
// between two implementations covers only its own rows; Float has rather more
// inputs than a fixture names.
//
// So the guard on the rule is an absolute assertion (convert_test.go) rather
// than a comparison against a second answer.

// FloatToInt is `std/float`'s `Float.to_int`: truncation toward zero, and
// `None` for anything Int cannot hold.
//
// Three rejections, and the third is the one that is easy to get wrong:
//
//   - NaN has no integer value at all.
//   - ±Inf has none either.
//   - The boundary is asymmetric. `float64(math.MaxInt64)` rounds up to 2^63,
//     which is one past Int's range, so a truncated value equal to it overflows
//     and the test is `>=`. `float64(math.MinInt64)` is exactly -2^63 and is
//     representable, so the low test is a strict `<`.
func FloatToInt(x float64) Maybe[Int] {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return None[Int]()
	}
	return Some(floatInt(math.Trunc(x)))
}

// floatInt is the Int a finite, whole Float is, exactly. A Float past int64's
// range is an integer times a power of two, so its Int is exact too.
func floatInt(whole float64) Int {
	if whole >= -float64(1<<63) && whole < float64(1<<63) {
		return SmallInt(int64(whole))
	}
	z, _ := big.NewFloat(whole).Int(nil)
	return BigIntOf(z)
}

// StringToInt is `std/strings`' `String.to_int`: a base-10 int64 parse of the
// string with surrounding whitespace trimmed, and `None` for anything else.
//
// Three details are the contract rather than incidental, because std/strings
// documents each with an executable example:
//
//   - surrounding whitespace is trimmed, so `"  3  "` is `Some(3)`;
//   - the base is 10, always — `"0x10"` is `None`, not 16;
//   - anything strconv rejects, including the empty string and a value outside
//     int64, is `None`. Nothing about why it failed reaches Nomi, which is what
//     keeps a Go error string out of Nomi-observable output.
func StringToInt(s string) Maybe[Int] {
	n, ok := ParseIntValue(strings.TrimSpace(s))
	if !ok {
		return None[Int]()
	}
	return Some(IntOf(n))
}

// StringToFloat is `std/strings`' `String.to_float`: a locale-free base-10
// parse, with surrounding whitespace trimmed as `String.to_int` trims it, and
// `None` for anything else. The grammar is checked here before strconv sees
// the text, because strconv.ParseFloat reads more than Nomi means:
//
//	[+-] digits [ "." digits ] [ ("e" | "E") [+-] digits ]
//
// so hex floats (`0x1p4`), `_` separators, `Inf`/`NaN` words, and a point
// with no digit on one side (`.5`, `5.`) are all `None`, as they are not
// Nomi Float literals either. A value whose magnitude rounds past the largest
// Float is `None` (strconv's ErrRange to ±Inf), as `Float.parse_in` answers
// `OutOfRange`; one too small to hold reads as zero or a subnormal, which is
// the nearest Float.
func StringToFloat(s string) Maybe[float64] {
	s = strings.TrimSpace(s)
	if !plainFloatSyntax(s) {
		return None[float64]()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !(math.Abs(f) < 1) {
		return None[float64]()
	}
	return Some(f)
}

// plainFloatSyntax reports whether s is `[+-] digits [. digits] [e [+-] digits]`.
func plainFloatSyntax(s string) bool {
	i := 0
	digits := func() bool {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i > start
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if !digits() {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if !digits() {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if !digits() {
			return false
		}
	}
	return i == len(s)
}
