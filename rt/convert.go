package rt

import (
	"math"
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
// So the guard on the rule is an ABSOLUTE assertion (convert_test.go) rather
// than a comparison against a second answer.

// FloatToInt is `std/float`'s `Float.to_int`: truncation toward zero, and
// `None` for anything Int cannot hold.
//
// Three rejections, and the third is the one that is easy to get wrong:
//
//   - NaN has no integer value at all.
//   - ±Inf has none either.
//   - The boundary is asymmetric. `float64(math.MaxInt64)` ROUNDS UP to 2^63,
//     which is one past Int's range, so a truncated value equal to it overflows
//     and the test is `>=`. `float64(math.MinInt64)` is exactly -2^63 and IS
//     representable, so the low test is a strict `<`.
func FloatToInt(x float64) Maybe[int64] {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return None[int64]()
	}
	truncated := math.Trunc(x)
	if truncated >= float64(math.MaxInt64) || truncated < float64(math.MinInt64) {
		return None[int64]()
	}
	return Some(int64(truncated))
}

// StringToInt is `std/strings`' `String.to_int`: a base-10 int64 parse of the
// string with surrounding whitespace trimmed, and `None` for anything else.
//
// Three details are the CONTRACT rather than incidental, because std/strings
// documents each with an executable example:
//
//   - surrounding whitespace is trimmed, so `"  3  "` is `Some(3)`;
//   - the base is 10, always — `"0x10"` is `None`, not 16;
//   - anything strconv rejects, including the empty string and a value outside
//     int64, is `None`. Nothing about WHY it failed reaches Nomi, which is what
//     keeps a Go error string out of Nomi-observable output.
func StringToInt(s string) Maybe[int64] {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return None[int64]()
	}
	return Some(n)
}
