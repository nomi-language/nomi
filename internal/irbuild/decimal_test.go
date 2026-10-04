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
// The report pin therefore names the `values:` ROWS and not the line. Those
// rows are a THIRD renderer — the assertion machinery's, independent of both
// `Display` and `Debug` — and no passing case can see a mistake in it, because
// a passing assertion renders nothing.

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

// TestPinned_DecimalAssertionOperands pins the THIRD renderer.
//
// The report is read for the two rows and the exit status, never for the line
// number: the line is a function of this fixture's comment lengths, and pinning
// it would make an unrelated edit above present as a Decimal regression.
//
// The rows are `1.50` and `2.25` — the DISPLAY form. That is the discriminating
// fact: an operand recorder that reached for `Debug.inspect` instead would
// render `1.50d`, and every other test in this file would still pass, because
// `Debug` is correct in its own right. This is the only place the two can
// contradict each other.
func TestPinned_DecimalAssertionOperands(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := vmReference(fixture("decimal.nomi"))
	if got.exit != 1 {
		t.Fatalf("the fixture's last case fails on purpose, so the run must exit 1: %s", got)
	}
	for _, want := range []string{
		"a failing Decimal comparison renders both operands",
		"      left\n        = 1.50\n",
		"      right\n        = 2.25\n",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the failure report must contain %q; report was:\n%s", want, got.stdout)
		}
	}
	// Scale-preserving, so NOT the normalized form. Stated in the negative
	// because the positive assertion above is satisfied by a substring of it.
	if strings.Contains(got.stdout, "= 1.5\n") {
		t.Errorf("the operand row dropped the trailing zero, so rendering normalized: %s", got.stdout)
	}
	// And not the Debug form, which is the other renderer.
	if strings.Contains(got.stdout, "= 1.50d") {
		t.Errorf("the operand row used Debug.inspect rather than Display: %s", got.stdout)
	}
}

// TestVM_MaybeDecimalEqual runs `Maybe.equal?` and `Result.equal?` over a
// Decimal payload. Maybe's derived Equatable and `==` both compare the payload
// with rt.EqDecimal, so the call must agree with `==`, across scales too.
func TestVM_MaybeDecimalEqual(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"equal? across scales = True",
		"== across scales = True",
		"equal? different = False",
		"equal? some none = False",
		"equal? none none = True",
		"result equal? across scales = True",
		"result equal? ok err = False",
		"",
	}, "\n")
	if got := vmReference(fixture("maybe_decimal_equal.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
