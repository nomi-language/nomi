package rt

import (
	"math/big"
	"testing"
)

// mustParse is a test helper that parses a decimal literal or fails the test.
func mustParse(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q) unexpected error: %v", s, err)
	}
	return d
}

func TestParseDecimalRoundTrip(t *testing.T) {
	// Display(ParseDecimal(s)) must reproduce the canonical form of s.
	// For inputs already in canonical form, that's s itself.
	tests := []struct {
		in   string
		want string // canonical Display form
	}{
		{"1.50", "1.50"}, // trailing zero preserved
		{"1.5", "1.5"},   // scale 1
		{"5", "5"},       // scale 0
		{"-0.001", "-0.001"},
		{"0.5", "0.5"},   // magnitude < 1
		{"0.05", "0.05"}, // magnitude < 1, extra fractional zero
		{"-0.5", "-0.5"},
		{"0", "0"},
		{"0.00", "0.00"},        // zero with scale
		{"-0", "0"},             // negative zero normalizes sign on display
		{"-0.0", "0.0"},         // negative zero with scale
		{"1_000.00", "1000.00"}, // underscore separators stripped
		{"1_000_000", "1000000"},
		{"123456789012345678901234567890.5", "123456789012345678901234567890.5"}, // beyond int64
		{"+5", "5"}, // explicit plus sign
		{"+1.50", "1.50"},
		{"100", "100"}, // integer trailing zeros kept (scale 0)
		{"100.00", "100.00"},
	}
	for _, tt := range tests {
		d, err := ParseDecimal(tt.in)
		if err != nil {
			t.Errorf("ParseDecimal(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got := d.Display(); got != tt.want {
			t.Errorf("Display(ParseDecimal(%q)) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseDecimalScale(t *testing.T) {
	tests := []struct {
		in    string
		scale int32
	}{
		{"1.50", 2},
		{"1.5", 1},
		{"5", 0},
		{"-0.001", 3},
		{"1_000.00", 2},
		{"100", 0},
	}
	for _, tt := range tests {
		d := mustParse(t, tt.in)
		if d.Scale != tt.scale {
			t.Errorf("ParseDecimal(%q).Scale = %d, want %d", tt.in, d.Scale, tt.scale)
		}
	}
}

func TestParseDecimalRejectsGarbage(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"abc",
		"1.2.3", // two decimal points
		"1.",    // trailing point, no fractional digits
		".5",    // leading point, no integer digits
		"1e3",   // exponent notation rejected
		"1.5e3", // exponent notation rejected
		"--5",   // double sign
		"+-5",   // mixed sign
		"5d",    // suffix not the value-layer's job (caller strips it)
		"1 000", // space separator
		"1,000", // comma separator
		"0x10",  // hex
		"_5",    // leading underscore (no leading digit)
		"-",     // sign only
		"+",     // sign only
		".",     // point only
	}
	for _, s := range bad {
		if _, err := ParseDecimal(s); err == nil {
			t.Errorf("ParseDecimal(%q) = nil error, want error", s)
		}
	}
}

func TestDecimalAddSub(t *testing.T) {
	tests := []struct {
		a, b, wantAdd, wantSub string
	}{
		{"1.50", "1.5", "3.00", "0.00"}, // scale = max(2,1) = 2
		{"1.5", "1.50", "3.00", "0.00"}, // commutative scale
		{"0.1", "0.2", "0.3", "-0.1"},   // the IEEE footgun, exact here
		{"5", "3", "8", "2"},            // scale 0
		{"10.00", "0.005", "10.005", "9.995"},
		{"-1.5", "2.5", "1.0", "-4.0"},
	}
	for _, tt := range tests {
		a := mustParse(t, tt.a)
		b := mustParse(t, tt.b)
		if got := a.Add(b).Display(); got != tt.wantAdd {
			t.Errorf("%s + %s = %q, want %q", tt.a, tt.b, got, tt.wantAdd)
		}
		if got := a.Sub(b).Display(); got != tt.wantSub {
			t.Errorf("%s - %s = %q, want %q", tt.a, tt.b, got, tt.wantSub)
		}
	}
}

func TestDecimalMul(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"1.50", "1.5", "2.250"},   // scale = 2 + 1 = 3
		{"5", "3", "15"},           // scale 0
		{"0.1", "0.1", "0.01"},     // scale 1 + 1 = 2
		{"-2.0", "3.00", "-6.000"}, // scale 1 + 2 = 3
		{"0", "5.50", "0.00"},      // zero keeps scale-sum
	}
	for _, tt := range tests {
		a := mustParse(t, tt.a)
		b := mustParse(t, tt.b)
		if got := a.Mul(b).Display(); got != tt.want {
			t.Errorf("%s * %s = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDecimalBigMantissa(t *testing.T) {
	// Values well beyond int64 range must be exact.
	a := mustParse(t, "9999999999999999999999999999.99")
	b := mustParse(t, "0.01")
	if got := a.Add(b).Display(); got != "10000000000000000000000000000.00" {
		t.Errorf("big add = %q", got)
	}
	// 12345678901234567890 * 98765432109876543210 (both > int64)
	x := mustParse(t, "12345678901234567890")
	y := mustParse(t, "98765432109876543210")
	want := new(big.Int)
	want.SetString("1219326311370217952237463801111263526900", 10)
	if x.Mul(y).Mantissa.Cmp(want) != 0 {
		t.Errorf("big mul = %s, want %s", x.Mul(y).Mantissa.String(), want.String())
	}
}

func TestDivExact(t *testing.T) {
	tests := []struct {
		a, b   string
		wantOK bool
		want   string // only checked when wantOK
	}{
		{"10.00", "4", true, "2.50"}, // preferred scale 2-0=2
		{"1", "8", true, "0.125"},    // 1/8 terminates; needs scale 3
		{"1", "4", true, "0.25"},
		{"10", "2", true, "5"},    // preferred scale 0
		{"7.5", "2.5", true, "3"}, // preferred scale 1-1=0
		{"1", "5", true, "0.2"},
		{"1", "2", true, "0.5"},
		{"1", "10", true, "0.1"},
		{"10", "3", false, ""}, // non-terminating (denom factor 3)
		{"1", "3", false, ""},
		{"1", "7", false, ""}, // denom factor 7
		{"2", "6", false, ""}, // reduces to 1/3
		{"0", "5", true, "0"}, // zero dividend
	}
	for _, tt := range tests {
		a := mustParse(t, tt.a)
		b := mustParse(t, tt.b)
		got, ok := a.DivExact(b)
		if ok != tt.wantOK {
			t.Errorf("DivExact(%s, %s) ok = %v, want %v", tt.a, tt.b, ok, tt.wantOK)
			continue
		}
		if ok {
			if got.Display() != tt.want {
				t.Errorf("DivExact(%s, %s) = %q, want %q", tt.a, tt.b, got.Display(), tt.want)
			}
			// Cross-check: result * b should equal a (value-wise).
			if got.Mul(b).Compare(a) != 0 {
				t.Errorf("DivExact(%s, %s)=%s * %s != %s", tt.a, tt.b, got.Display(), tt.b, tt.a)
			}
		}
	}
}

func TestDivExactReducedDenominator(t *testing.T) {
	// 6/8 reduces to 3/4 — denom 4 = 2^2, terminating → 0.75.
	a := mustParse(t, "6")
	b := mustParse(t, "8")
	got, ok := a.DivExact(b)
	if !ok || got.Display() != "0.75" {
		t.Errorf("6/8 = %q ok=%v, want 0.75 ok=true", got.Display(), ok)
	}
}

// roundingVectors is the canonical Java RoundingMode javadoc table, rounding
// each input to scale 0. Cross-checked against Python's decimal module.
func TestRound(t *testing.T) {
	type modeWant struct {
		mode RoundingMode
		want string
	}
	// Java RoundingMode summary table (scale 0).
	tests := []struct {
		in    string
		modes []modeWant
	}{
		{"5.5", []modeWant{
			{RoundUp, "6"}, {RoundDown, "5"}, {RoundCeiling, "6"}, {RoundFloor, "5"},
			{RoundHalfUp, "6"}, {RoundHalfDown, "5"}, {RoundHalfEven, "6"},
		}},
		{"2.5", []modeWant{
			{RoundUp, "3"}, {RoundDown, "2"}, {RoundCeiling, "3"}, {RoundFloor, "2"},
			{RoundHalfUp, "3"}, {RoundHalfDown, "2"}, {RoundHalfEven, "2"},
		}},
		{"1.6", []modeWant{
			{RoundUp, "2"}, {RoundDown, "1"}, {RoundCeiling, "2"}, {RoundFloor, "1"},
			{RoundHalfUp, "2"}, {RoundHalfDown, "2"}, {RoundHalfEven, "2"},
		}},
		{"1.1", []modeWant{
			{RoundUp, "2"}, {RoundDown, "1"}, {RoundCeiling, "2"}, {RoundFloor, "1"},
			{RoundHalfUp, "1"}, {RoundHalfDown, "1"}, {RoundHalfEven, "1"},
		}},
		{"1.0", []modeWant{
			{RoundUp, "1"}, {RoundDown, "1"}, {RoundCeiling, "1"}, {RoundFloor, "1"},
			{RoundHalfUp, "1"}, {RoundHalfDown, "1"}, {RoundHalfEven, "1"},
		}},
		{"-1.0", []modeWant{
			{RoundUp, "-1"}, {RoundDown, "-1"}, {RoundCeiling, "-1"}, {RoundFloor, "-1"},
			{RoundHalfUp, "-1"}, {RoundHalfDown, "-1"}, {RoundHalfEven, "-1"},
		}},
		{"-1.1", []modeWant{
			{RoundUp, "-2"}, {RoundDown, "-1"}, {RoundCeiling, "-1"}, {RoundFloor, "-2"},
			{RoundHalfUp, "-1"}, {RoundHalfDown, "-1"}, {RoundHalfEven, "-1"},
		}},
		{"-1.6", []modeWant{
			{RoundUp, "-2"}, {RoundDown, "-1"}, {RoundCeiling, "-1"}, {RoundFloor, "-2"},
			{RoundHalfUp, "-2"}, {RoundHalfDown, "-2"}, {RoundHalfEven, "-2"},
		}},
		{"-2.5", []modeWant{
			{RoundUp, "-3"}, {RoundDown, "-2"}, {RoundCeiling, "-2"}, {RoundFloor, "-3"},
			{RoundHalfUp, "-3"}, {RoundHalfDown, "-2"}, {RoundHalfEven, "-2"},
		}},
		{"-5.5", []modeWant{
			{RoundUp, "-6"}, {RoundDown, "-5"}, {RoundCeiling, "-5"}, {RoundFloor, "-6"},
			{RoundHalfUp, "-6"}, {RoundHalfDown, "-5"}, {RoundHalfEven, "-6"},
		}},
		// Half-even tie at scale 0 that rounds to an even digit by going up.
		{"0.5", []modeWant{
			{RoundHalfEven, "0"}, {RoundHalfUp, "1"}, {RoundHalfDown, "0"},
		}},
		{"3.5", []modeWant{
			{RoundHalfEven, "4"}, {RoundHalfDown, "3"},
		}},
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		for _, mw := range tt.modes {
			got, err := in.Round(0, mw.mode)
			if err != nil {
				t.Errorf("Round(%s, 0, %v) error: %v", tt.in, mw.mode, err)
				continue
			}
			if got.Display() != mw.want {
				t.Errorf("Round(%s, 0, %v) = %q, want %q", tt.in, mw.mode, got.Display(), mw.want)
			}
			// Result must have the requested scale.
			if got.Scale != 0 {
				t.Errorf("Round(%s, 0, %v) scale = %d, want 0", tt.in, mw.mode, got.Scale)
			}
		}
	}
}

func TestRoundToScale2(t *testing.T) {
	// Rounding to a fractional scale, mainly money-shaped.
	tests := []struct {
		in   string
		mode RoundingMode
		want string
	}{
		{"1.005", RoundHalfUp, "1.01"},
		{"1.005", RoundHalfEven, "1.00"}, // exact half; kept digit (the 0 in 1.00) is even → ties stay, so down to 1.00
		{"1.015", RoundHalfEven, "1.02"}, // digit before is 1(odd) → up
		{"2.345", RoundHalfEven, "2.34"}, // digit before is 4(even) → down
		{"2.355", RoundHalfEven, "2.36"}, // digit before is 5(odd) → up
		{"1.234", RoundDown, "1.23"},
		{"1.236", RoundDown, "1.23"},
		{"1.234", RoundUp, "1.24"},
		{"-1.234", RoundUp, "-1.24"},
		{"-1.234", RoundDown, "-1.23"},
		{"1.5", RoundHalfEven, "1.50"}, // increasing scale: no rounding, pad zeros
		{"1.2", RoundUp, "1.20"},       // increasing scale
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		got, err := in.Round(2, tt.mode)
		if err != nil {
			t.Errorf("Round(%s, 2, %v) error: %v", tt.in, tt.mode, err)
			continue
		}
		if got.Display() != tt.want {
			t.Errorf("Round(%s, 2, %v) = %q, want %q", tt.in, tt.mode, got.Display(), tt.want)
		}
	}
}

// TestDisplayNegativeScale checks that a value with negative Scale (which only
// arises from rounding to tens/hundreds/…) renders as the integer
// Mantissa × 10^(-Scale) — mantissa digits followed by (-Scale) zeros.
func TestDisplayNegativeScale(t *testing.T) {
	tests := []struct {
		mant  int64
		scale int32
		want  string
	}{
		{12, -2, "1200"}, // 12 × 10^2
		{12, -1, "120"},
		{123, -1, "1230"},
		{-12, -2, "-1200"}, // sign preserved
		{5, -3, "5000"},
		{0, -2, "0"}, // zero stays "0", no padding
		{0, -1, "0"},
	}
	for _, tt := range tests {
		d := Decimal{Mantissa: big.NewInt(tt.mant), Scale: tt.scale}
		if got := d.Display(); got != tt.want {
			t.Errorf("Display({%d, %d}) = %q, want %q", tt.mant, tt.scale, got, tt.want)
		}
	}
}

// TestRoundNegativeScale verifies rounding to a negative scale (round to
// tens/hundreds). Expected values cross-checked against Java BigDecimal.setScale
// and Python decimal.quantize.
func TestRoundNegativeScale(t *testing.T) {
	tests := []struct {
		in    string
		scale int32
		mode  RoundingMode
		want  string
	}{
		{"1234", -2, RoundHalfUp, "1200"}, // 12 rem 34: below half → down
		{"1250", -2, RoundHalfUp, "1300"}, // exact half → away
		{"1234.56", -2, RoundHalfUp, "1200"},
		{"1234", -1, RoundHalfEven, "1230"}, // 123 rem 4 → down
		{"1250", -2, RoundHalfEven, "1200"}, // tie, kept digit 12 even → stays
		{"1350", -2, RoundHalfEven, "1400"}, // tie, kept digit 13 odd → away
		{"-1250", -2, RoundHalfUp, "-1300"}, // negative tie → away from zero
		{"1234", -2, RoundDown, "1200"},     // truncate
		{"1299", -2, RoundUp, "1300"},       // any remainder → away
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		got, err := in.Round(tt.scale, tt.mode)
		if err != nil {
			t.Errorf("Round(%s, %d, %v) error: %v", tt.in, tt.scale, tt.mode, err)
			continue
		}
		if got.Display() != tt.want {
			t.Errorf("Round(%s, %d, %v) = %q, want %q", tt.in, tt.scale, tt.mode, got.Display(), tt.want)
		}
		if got.Scale != tt.scale {
			t.Errorf("Round(%s, %d, %v) scale = %d, want %d", tt.in, tt.scale, tt.mode, got.Scale, tt.scale)
		}
	}
}

// TestDivRoundNegativeScale verifies division rounded to a negative scale.
// 2500/2 = 1250; rounded to scale -2 HalfUp is an exact half → 1300.
func TestDivRoundNegativeScale(t *testing.T) {
	a := mustParse(t, "2500")
	b := mustParse(t, "2")
	got, err := a.DivRound(b, -2, RoundHalfUp)
	if err != nil {
		t.Fatalf("DivRound(2500, 2, -2, HalfUp) error: %v", err)
	}
	if got.Display() != "1300" {
		t.Errorf("DivRound(2500, 2, -2, HalfUp) = %q, want 1300", got.Display())
	}
	if got.Scale != -2 {
		t.Errorf("DivRound(2500, 2, -2, HalfUp) scale = %d, want -2", got.Scale)
	}
}

// TestRoundSmallMagnitude exercises the quotient-is-zero rounding paths
// (rounding a value with |v| < 1 down to scale 0), including negatives, where
// the sign drives the direction even though the truncated quotient is 0.
func TestRoundSmallMagnitude(t *testing.T) {
	tests := []struct {
		in   string
		mode RoundingMode
		want string
	}{
		{"0.4", RoundUp, "1"},
		{"0.4", RoundDown, "0"},
		{"0.4", RoundHalfUp, "0"},
		{"0.6", RoundHalfUp, "1"},
		{"-0.4", RoundUp, "-1"},  // away from zero
		{"-0.4", RoundDown, "0"}, // toward zero
		{"-0.4", RoundCeiling, "0"},
		{"-0.4", RoundFloor, "-1"},
		{"0.4", RoundCeiling, "1"},
		{"0.4", RoundFloor, "0"},
		{"-0.5", RoundHalfEven, "0"}, // tie, kept digit 0 is even
		{"-0.5", RoundHalfUp, "-1"},
		{"0.5", RoundHalfEven, "0"},
		{"-1.5", RoundHalfEven, "-2"}, // tie, kept digit -1 odd → away
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		got, err := in.Round(0, tt.mode)
		if err != nil {
			t.Errorf("Round(%s, 0, %v) error: %v", tt.in, tt.mode, err)
			continue
		}
		if got.Display() != tt.want {
			t.Errorf("Round(%s, 0, %v) = %q, want %q", tt.in, tt.mode, got.Display(), tt.want)
		}
	}
}

func TestRoundUnnecessary(t *testing.T) {
	// Exact at the target scale → ok.
	in := mustParse(t, "1.50")
	got, err := in.Round(2, RoundUnnecessary)
	if err != nil {
		t.Fatalf("Round(1.50, 2, Unnecessary) error: %v", err)
	}
	if got.Display() != "1.50" {
		t.Errorf("Round(1.50, 2, Unnecessary) = %q, want 1.50", got.Display())
	}
	// Exact when reducing scale (trailing zeros only discarded).
	got, err = mustParse(t, "1.500").Round(1, RoundUnnecessary)
	if err != nil {
		t.Fatalf("Round(1.500, 1, Unnecessary) error: %v", err)
	}
	if got.Display() != "1.5" {
		t.Errorf("Round(1.500, 1, Unnecessary) = %q, want 1.5", got.Display())
	}
	// Increasing scale is always exact.
	if _, err := mustParse(t, "1.5").Round(3, RoundUnnecessary); err != nil {
		t.Errorf("Round(1.5, 3, Unnecessary) error: %v", err)
	}
	// Inexact → error.
	if _, err := mustParse(t, "1.555").Round(2, RoundUnnecessary); err == nil {
		t.Errorf("Round(1.555, 2, Unnecessary) = nil error, want error")
	}
	if _, err := mustParse(t, "10").Round(0, RoundUnnecessary); err != nil {
		t.Errorf("Round(10, 0, Unnecessary) error: %v", err)
	}
}

func TestDivRound(t *testing.T) {
	tests := []struct {
		a, b  string
		scale int32
		mode  RoundingMode
		want  string
	}{
		{"10", "3", 2, RoundHalfEven, "3.33"},
		{"10", "3", 4, RoundHalfEven, "3.3333"},
		{"2", "3", 2, RoundHalfUp, "0.67"},
		{"1", "3", 0, RoundHalfUp, "0"},
		{"1", "3", 0, RoundUp, "1"},
		{"10.00", "4", 2, RoundHalfEven, "2.50"}, // exact, but explicit-scale path
		{"-10", "3", 2, RoundHalfEven, "-3.33"},
		{"100", "8", 2, RoundHalfEven, "12.50"},
		{"22", "7", 6, RoundHalfEven, "3.142857"},
		{"-1", "3", 0, RoundHalfUp, "0"},    // -0.333.. → 0 (zero quotient, negative)
		{"-1", "3", 0, RoundUp, "-1"},       // away from zero
		{"-2", "3", 0, RoundHalfUp, "-1"},   // -0.666.. → -1
		{"1", "8", 1, RoundHalfEven, "0.1"}, // 0.125 → 0.1 (kept digit 1 odd? cmp<0 so down)
	}
	for _, tt := range tests {
		a := mustParse(t, tt.a)
		b := mustParse(t, tt.b)
		got, err := a.DivRound(b, tt.scale, tt.mode)
		if err != nil {
			t.Errorf("DivRound(%s, %s, %d, %v) error: %v", tt.a, tt.b, tt.scale, tt.mode, err)
			continue
		}
		if got.Display() != tt.want {
			t.Errorf("DivRound(%s, %s, %d, %v) = %q, want %q", tt.a, tt.b, tt.scale, tt.mode, got.Display(), tt.want)
		}
		if got.Scale != tt.scale {
			t.Errorf("DivRound(%s, %s, %d, %v) scale = %d, want %d", tt.a, tt.b, tt.scale, tt.mode, got.Scale, tt.scale)
		}
	}
}

func TestDivRoundByZero(t *testing.T) {
	a := mustParse(t, "1")
	z := mustParse(t, "0")
	if _, err := a.DivRound(z, 2, RoundHalfEven); err == nil {
		t.Errorf("DivRound(1, 0, ...) = nil error, want division-by-zero error")
	}
	// 0.00 is also zero (scale doesn't matter).
	z2 := mustParse(t, "0.00")
	if _, err := a.DivRound(z2, 2, RoundHalfEven); err == nil {
		t.Errorf("DivRound(1, 0.00, ...) = nil error, want division-by-zero error")
	}
}

func TestDivRoundUnnecessary(t *testing.T) {
	// Exact quotient at the requested scale → ok.
	got, err := mustParse(t, "10").DivRound(mustParse(t, "4"), 2, RoundUnnecessary)
	if err != nil {
		t.Fatalf("DivRound(10, 4, 2, Unnecessary) error: %v", err)
	}
	if got.Display() != "2.50" {
		t.Errorf("DivRound(10, 4, 2, Unnecessary) = %q, want 2.50", got.Display())
	}
	// Non-terminating → error.
	if _, err := mustParse(t, "10").DivRound(mustParse(t, "3"), 4, RoundUnnecessary); err == nil {
		t.Errorf("DivRound(10, 3, 4, Unnecessary) = nil error, want error")
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in        string
		wantDisp  string
		wantScale int32
	}{
		{"1.50", "1.5", 1},   // strip one fractional zero
		{"1.500", "1.5", 1},  // strip two
		{"100", "100", 0},    // integer trailing zeros NOT stripped (scale 0)
		{"100.00", "100", 0}, // fractional zeros stripped down to scale 0
		{"0", "0", 0},
		{"0.00", "0", 0}, // zero normalizes to mantissa 0, scale 0
		{"-0.00", "0", 0},
		{"0.0500", "0.05", 2},
		{"5", "5", 0},
		{"-2.50", "-2.5", 1},
		{"10.10", "10.1", 1},
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		got := in.Normalize()
		if got.Display() != tt.wantDisp {
			t.Errorf("Normalize(%s).Display() = %q, want %q", tt.in, got.Display(), tt.wantDisp)
		}
		if got.Scale != tt.wantScale {
			t.Errorf("Normalize(%s).Scale = %d, want %d", tt.in, got.Scale, tt.wantScale)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.50", "1.5", 0}, // scale-insensitive equal
		{"1.5", "1.50", 0},
		{"1.5", "1.6", -1},
		{"1.6", "1.5", 1},
		{"-1.5", "1.5", -1},
		{"0", "0.00", 0},
		{"100", "100.00", 0},
		{"0.1", "0.10", 0},
		{"-2", "-3", 1},
		{"1000000000000000000000", "999999999999999999999", 1}, // big
	}
	for _, tt := range tests {
		a := mustParse(t, tt.a)
		b := mustParse(t, tt.b)
		if got := a.Compare(b); got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSignAbsNegateIsZero(t *testing.T) {
	tests := []struct {
		in     string
		sign   int
		abs    string
		negate string
		isZero bool
	}{
		{"1.50", 1, "1.50", "-1.50", false},
		{"-1.50", -1, "1.50", "1.50", false},
		{"0", 0, "0", "0", true},
		{"0.00", 0, "0.00", "0.00", true}, // zero with scale stays zero, sign 0
		{"-0.001", -1, "0.001", "0.001", false},
	}
	for _, tt := range tests {
		in := mustParse(t, tt.in)
		if got := in.Sign(); got != tt.sign {
			t.Errorf("Sign(%s) = %d, want %d", tt.in, got, tt.sign)
		}
		if got := in.Abs().Display(); got != tt.abs {
			t.Errorf("Abs(%s) = %q, want %q", tt.in, got, tt.abs)
		}
		if got := in.Negate().Display(); got != tt.negate {
			t.Errorf("Negate(%s) = %q, want %q", tt.in, got, tt.negate)
		}
		if got := in.IsZero(); got != tt.isZero {
			t.Errorf("IsZero(%s) = %v, want %v", tt.in, got, tt.isZero)
		}
	}
}
