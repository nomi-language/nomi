package analysis_test

import (
	"strings"
	"testing"
)

// A decimal literal types as Decimal, and the blessed operators on two
// Decimals type-check cleanly.
func TestDecimalLiteralsAndOperatorsTypecheck(t *testing.T) {
	src := `fn f() -> Bool {
  sum = 1.50d + 1.5d
  prod = 1.50d * 1.5d
  quot = Decimal.divide_exact(10.00d, 4d)
  diff = 2.50d - 1d
  sum == 3.00d and prod == 2.250d and quot == Some(2.50d) and diff == 1.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

const decimalDivisionText = "Decimal has no `/`: write `Decimal.divide(a, b, scale, mode)` to round, " +
	"or `Decimal.divide_exact(a, b)` for a quotient that must terminate"

// Decimal has no `/`. Each division with a Decimal operand, written as the
// operator or through `Divide.divide`, is one error at the operator (or the
// call), and it names the two divisions std/decimal declares rather than the
// missing `Divide` impl. An Int or Float on the other side gets the same
// help, not the operand mismatch.
func TestDecimalDivisionRejected(t *testing.T) {
	src := `fn quotient(x: Decimal, y: Decimal) -> Decimal {
  x / y
}

fn f() -> Decimal {
  a = 1d / 3d
  b = 1d / 3
  c = 1 / 3d
  d = 1.5 / 3d
  e = Divide.divide(1d, 3d)
  a + b + c + d + e
}
`
	_, errs := checkSourceWithStdlib(src)
	errs = withoutUnusedBindingErrors(errs)
	type at struct{ line, col int }
	want := []at{{2, 5}, {6, 10}, {7, 10}, {8, 9}, {9, 11}, {10, 7}}
	var got []at
	for _, e := range errs {
		if e.Message != decimalDivisionText {
			t.Errorf("%d:%d: %q, want the Decimal division error", e.Line, e.Col, e.Message)
			continue
		}
		got = append(got, at{e.Line, e.Col})
	}
	if len(got) != len(want) {
		t.Fatalf("Decimal division errors at %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Decimal division error %d at %v, want %v", i, got[i], want[i])
		}
	}
}

// Generic code whose bound a Decimal cannot meet is rejected at the call:
// Decimal implements no `Divide`.
func TestDecimalDoesNotSatisfyADivideBound(t *testing.T) {
	src := `fn half<T>(x: T, two: T) -> T where T: Divide<T, T> {
  x / two
}

fn f() -> Decimal {
  half(3d, 2d)
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "Divide")
}

// A Decimal on the right of a type with its own `Divide<Decimal, _>` impl is
// that impl's to answer, not the Decimal division error.
func TestDecimalDivisorOfAUserTypeUsesItsImpl(t *testing.T) {
	src := `struct Money {
  amount: Decimal
}

impl Divide<Decimal, Money> for Money {
  fn divide(lhs: Money, rhs: Decimal) -> Money {
    #Money{amount: Decimal.divide_exact(lhs.amount, rhs) |> Maybe.with_default(0d)}
  }
}

fn f() -> Money {
  #Money{amount: 10d} / 4d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Comparisons and equality between two Decimals type-check (Bool result),
// including the scale-insensitive equality 1.50d == 1.5d.
func TestDecimalComparisonsTypecheck(t *testing.T) {
	src := `fn f() -> Bool {
  a = 1.50d < 2d
  b = 1.50d == 1.5d
  c = 1.50d >= 1.5d
  a and b and c
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Mixed Int/Decimal arithmetic is rejected (no implicit conversion).
func TestDecimalMixedArithmeticRejected(t *testing.T) {
	src := `fn f() -> Decimal {
  1 + 1.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Float/Decimal equality is rejected.
func TestDecimalMixedEqualityRejected(t *testing.T) {
	src := `fn f() -> Bool {
  1.0 == 1d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Int/Decimal comparison is rejected.
func TestDecimalMixedComparisonRejected(t *testing.T) {
	src := `fn f() -> Bool {
  1 < 2.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Modulo is not defined on Decimal — a Decimal % Decimal is a compile error.
func TestDecimalModuloRejected(t *testing.T) {
	src := `fn f() -> Decimal {
  5.0d % 2d
}
`
	_, errs := checkSourceWithStdlib(src)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "modulo") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected a 'modulo is not defined on Decimal' error, got %d errors:\n  %s",
			len(errs), strings.Join(msgs, "\n  "))
	}
}
