package irbuild

import (
	"strings"
	"testing"
)

// `Decimal`'s observable rules, pinned ABSOLUTELY.
//
// # Why these are absolute
//
// `Decimal` is the third of three blessed primitives `scalarKind` does not
// handle, and it is `rt.Decimal`: one implementation of exact base-10
// arithmetic. Nothing compares it against a second, so something has to say
// what the one implementation must produce, and these pins do.
//
// # What is pinned, and what is deliberately not
//
// The rendering pin reads a fixture with NO assertions. That is the whole
// reason `decimal_render.nomi` exists beside `decimal.nomi`: a test report
// embeds the failing source LINE, which makes the blob a function of its
// neighbours' comment lengths — the exact failure differential_test.go's own
// header records twice ("an absolute pin carried SSA NUMERALS"; "a fixture
// asserted `observed.line == 27` about ITSELF"). A program's stdout is a
// function of the rendering rules and nothing else.
//
// A Decimal's `values:` row is its Debug, `1.50d`, which
// TestStdHostTypeRowsRenderAsDebug holds, and decimal.nomi's failing case,
// recorded in the golden files, pins as a report.

// TestPinned_DecimalRendering is the absolute statement of every rendering rule.
//
// Every line is derived from the SPEC rather than read off the implementation,
// and one of them is here because deriving it first caught a mistake in this
// file's own author: `Decimal.to_int(min + 0.5d)` is NOT `min` — truncation is
// toward zero, so adding half a unit to the MINIMUM moves toward zero and
// truncates one short. The corrected pair (in decimal.nomi) is sharper than the
// wrong one, because a half-step OUTSIDE the boundary truncating back INSIDE it
// is what distinguishes "truncate then range-check" from "range-check then
// truncate".
func TestPinned_DecimalRendering(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		// Exactness. A float64 renders these "0.30000000000000004",
		// "0.020000000000000004" and "0.6000000000000001".
		"0.3\n" +
		"0.02\n" +
		"0.6\n" +
		// Scale is preserved, and arithmetic GROWS it: 2+1 digits for a sum
		// aligns to 2, and a product's scale is the SUM of its operands'.
		"1.50\n" +
		"3.00\n" +
		"2.250\n" +
		"2.50\n" +
		// Display against Debug on one value. The `d` is the entire difference
		// and it is deliberate: a Decimal must be distinguishable from a Float
		// and must paste back as a literal.
		"1.50\n" +
		"1.50d\n" +
		// Zero renders without a point; NEGATIVE zero keeps its scale and
		// loses its sign, which is a rule and not an accident.
		"0\n" +
		"0.0\n" +
		"-0.001\n" +
		"-0.001d\n" +
		// Arbitrary precision: a mantissa far past int64, exactly.
		"1234567890123456789012345.00000\n" +
		// Int64's minimum, and one below it — a value Int cannot hold.
		"-9223372036854775808\n" +
		"-9223372036854775809\n"
	got := vmReference(fixture("decimal_render.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("Decimal's rendering rules are not what this pins:\nwant stdout=%q\ngot  %s",
			want, got)
	}
}

// TestVM_OptionDecimalEqual runs `Option.equal` and `Result.equal` over a
// Decimal payload. Option's derived Equatable and `==` both compare the payload
// with rt.EqDecimal, so the call must agree with `==`, across scales too.
func TestVM_OptionDecimalEqual(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"equal across scales = True",
		"== across scales = True",
		"equal different = False",
		"equal some none = False",
		"equal none none = True",
		"result equal across scales = True",
		"result equal ok err = False",
		"",
	}, "\n")
	if got := vmReference(fixture("option_decimal_equal.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
