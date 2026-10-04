package rt

import (
	"math"
	"strconv"
	"strings"
)

// How a scalar renders — in `${...}` and through `io.print` — is observable, so
// it lives here once and every renderer calls it. See the package doc.

// FormatInt renders an Int.
func FormatInt(v int64) string { return strconv.FormatInt(v, 10) }

// FormatBool renders a Bool as "True" or "False".
//
// This is one of the two places in rt that genuinely re-encodes a rule std
// states elsewhere, so it is worth being precise about what the rule is.
// `Bool` is not a primitive: it is `pub enum Bool { embeds False; embeds
// True }` (std/bool.nomi:27-30) carrying `derive Display`, and the impl that
// derive synthesizes prints the bare variant name for a zero-sized embed. So
// "True" here is not a Go convention chosen to look like Nomi's — it is the
// output of a derived Nomi impl, transcribed. It is pinned by
// testdata/arithmetic.nomi.
func FormatBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// FormatFloat renders a Float in the Gleam style Nomi uses: full decimal
// expansion for everyday magnitudes, scientific notation only at the extremes,
// and a forced trailing ".0" on whole numbers so a reader can see at a glance
// that the value is a Float and not an Int.
//
// The ".0" is load-bearing and is the reason this cannot be `%v` or `%g`:
// `1.0` prints "1.0", and `3.0` is pinned byte-for-byte by
// testdata/arithmetic.nomi.
func FormatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	abs := math.Abs(f)
	// Threshold mirrors Erlang's `~w`/`float_to_binary [short]`: expand for
	// everyday magnitudes, switch to scientific only when the expanded form
	// would be unwieldy (1e21 has 22 digits; below 1e-4 the leading zeros start
	// to obscure the value).
	if abs != 0 && (abs < 1e-4 || abs >= 1e21) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		i := strings.IndexByte(s, 'e')
		mantissa, exp := s[:i], s[i+1:]
		sign := ""
		if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
			if exp[0] == '-' {
				sign = "-"
			}
			exp = exp[1:]
		}
		// Go zero-pads single-digit exponents (`1e-05`); strip those so single
		// digit exponents read as `1.0e-5` like Erlang/Elixir.
		for len(exp) > 1 && exp[0] == '0' {
			exp = exp[1:]
		}
		if !strings.ContainsRune(mantissa, '.') {
			mantissa += ".0"
		}
		return mantissa + "e" + sign + exp
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
