package analysis_test

import (
	"strings"
	"testing"
)

// A decimal literal types as Decimal, and the blessed operators on two
// Decimals type-check cleanly.
func TestDecimalLiteralsAndOperatorsTypecheck(t *testing.T) {
	src := `fn f(): Bool {
  sum = 1.50d + 1.5d
  prod = 1.50d * 1.5d
  quot = 10.00d / 4d
  diff = 2.50d - 1d
  sum == 3.00d and prod == 2.250d and quot == 2.50d and diff == 1.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Comparisons and equality between two Decimals type-check (Bool result),
// including the scale-insensitive equality 1.50d == 1.5d.
func TestDecimalComparisonsTypecheck(t *testing.T) {
	src := `fn f(): Bool {
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
	src := `fn f(): Decimal {
  1 + 1.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Float/Decimal equality is rejected.
func TestDecimalMixedEqualityRejected(t *testing.T) {
	src := `fn f(): Bool {
  1.0 == 1d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Int/Decimal comparison is rejected.
func TestDecimalMixedComparisonRejected(t *testing.T) {
	src := `fn f(): Bool {
  1 < 2.50d
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Modulo is not defined on Decimal — a Decimal % Decimal is a compile error.
func TestDecimalModuloRejected(t *testing.T) {
	src := `fn f(): Decimal {
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
