package rt

import (
	"math"
	"reflect"
	"testing"
)

// The conversions in convert.go are the one implementation of their rules, so
// a fixture comparing two callers cannot fail on a bug in here: both would be
// wrong in the same place and would agree. Every assertion below therefore
// spells out the answer instead of comparing two computations.
//
// That is the same reasoning prelude_test.go states for the tag constants: when
// two callers share one wrong implementation, a fixture comparing them passes.

func TestFloatToInt_Absolute(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
		some bool
		want int64
	}{
		// Truncation is toward zero, not floor: -3.7 is -3, not -4. The two
		// disagree for every negative non-integer, which is most of the domain.
		{"positive truncates toward zero", 3.7, true, 3},
		{"negative truncates toward zero", -3.7, true, -3},
		{"exact", 42.0, true, 42},
		{"zero", 0.0, true, 0},
		{"negative zero is zero", math.Copysign(0, -1), true, 0},
		{"just under one", 0.9999, true, 0},
		{"just over minus one", -0.9999, true, 0},

		{"NaN has no integer value", math.NaN(), false, 0},
		{"positive infinity", math.Inf(1), false, 0},
		{"negative infinity", math.Inf(-1), false, 0},

		// The boundary is asymmetric and this is the pair that proves it.
		// float64(math.MinInt64) is exactly -2^63 and is representable;
		// float64(math.MaxInt64) rounds up to 2^63, which is one past the range,
		// so the largest float64 below it is the largest accepted value.
		{"exactly MinInt64 is in range", math.MinInt64, true, math.MinInt64},
		{"one step below MinInt64 is out", math.Nextafter(math.MinInt64, math.Inf(-1)), false, 0},
		{"float64(MaxInt64) is OUT, because it rounded up", float64(math.MaxInt64), false, 0},
		{"the float below it is in", math.Nextafter(float64(math.MaxInt64), 0), true, 9223372036854774784},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FloatToInt(tc.in)
			if !tc.some {
				if got.Tag != TagNone {
					t.Fatalf("FloatToInt(%v) = Some(%d), want None", tc.in, got.Some)
				}
				return
			}
			if got.Tag != TagSome {
				t.Fatalf("FloatToInt(%v) = None, want Some(%d)", tc.in, tc.want)
			}
			if got.Some != tc.want {
				t.Fatalf("FloatToInt(%v) = Some(%d), want Some(%d)", tc.in, got.Some, tc.want)
			}
		})
	}
}

func TestStringToInt_Absolute(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		some bool
		want int64
	}{
		{"plain", "42", true, 42},
		{"negative", "-7", true, -7},
		{"explicit plus", "+7", true, 7},
		// std/strings documents this one with an executable example, so the
		// trimming is a contract rather than an implementation detail.
		{"surrounding whitespace is trimmed", "  3  ", true, 3},
		{"tabs and newlines too", "\t9\n", true, 9},
		{"leading zeros", "007", true, 7},
		{"MinInt64", "-9223372036854775808", true, math.MinInt64},
		{"MaxInt64", "9223372036854775807", true, math.MaxInt64},

		{"empty", "", false, 0},
		{"blank", "   ", false, 0},
		{"letters", "abc", false, 0},
		// The base is 10, always. strconv.ParseInt with base 0 would read this
		// as 16, which would be a different language.
		{"hex is not accepted", "0x10", false, 0},
		{"binary is not accepted", "0b11", false, 0},
		{"underscore separators are not accepted", "1_000", false, 0},
		{"interior whitespace", "1 2", false, 0},
		{"a float", "3.5", false, 0},
		{"one past MaxInt64", "9223372036854775808", false, 0},
		{"one past MinInt64", "-9223372036854775809", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := StringToInt(tc.in)
			if !tc.some {
				if got.Tag != TagNone {
					t.Fatalf("StringToInt(%q) = Some(%d), want None", tc.in, got.Some)
				}
				return
			}
			if got.Tag != TagSome {
				t.Fatalf("StringToInt(%q) = None, want Some(%d)", tc.in, tc.want)
			}
			if got.Some != tc.want {
				t.Fatalf("StringToInt(%q) = Some(%d), want Some(%d)", tc.in, got.Some, tc.want)
			}
		})
	}
}

// TestRefinedTypesAreNominallyDistinct is the property that makes the two
// refinement newtypes worth having Go types for at all.
//
// `NonZeroInt` and `PositiveInt` are both defined over int64, so a Go
// implementation that used int64 directly would compile and would let a plain
// Int flow into `Int.divide`'s divisor — losing the proof the type carries. Go's
// own type checker enforces it once they are defined types, and this asserts
// that they are (a `type X = int64` alias would pass a value-equality test and
// fail this one).
func TestRefinedTypesAreNominallyDistinct(t *testing.T) {
	var nz NonZeroInt = 5
	var pos PositiveInt = 5
	if int64(nz) != 5 || int64(pos) != 5 {
		t.Fatal("a refinement newtype does not carry its int64 value")
	}
	// Reflectively, because the assignments Go would reject cannot be written.
	if got := reflect.TypeOf(nz).Name(); got != "NonZeroInt" {
		t.Fatalf("NonZeroInt reflects as %q; an alias would report int64", got)
	}
	if got := reflect.TypeOf(pos).Name(); got != "PositiveInt" {
		t.Fatalf("PositiveInt reflects as %q; an alias would report int64", got)
	}
}
