package rt

import (
	"fmt"
	"math"
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
func FloatRound(x float64) float64 { return math.RoundToEven(x) }

// FloatFloor is `Float.floor`.
func FloatFloor(x float64) float64 { return math.Floor(x) }

// FloatCeil is `Float.ceil`.
func FloatCeil(x float64) float64 { return math.Ceil(x) }

// FloatTrunc is `Float.trunc`.
func FloatTrunc(x float64) float64 { return math.Trunc(x) }

// FloatBits is std/float's private `float_bits`: the IEEE-754 bit pattern as an
// Int, backing the Float `Hashable` impl.
func FloatBits(x float64) int64 { return int64(math.Float64bits(x)) }
