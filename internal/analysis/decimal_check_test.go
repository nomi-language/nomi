package analysis_test

import "testing"

// A decimal literal types as Decimal, and the blessed operators on two
// Decimals type-check cleanly.
func TestDecimalLiteralsAndOperatorsTypecheck(t *testing.T) {
	src := `fn f() -> Bool
  sum = 1.50d + 1.5d
  prod = 1.50d * 1.5d
  quot = Decimal.divide_exact(10.00d, 4d)
  diff = 2.50d - 1d
  sum == 3.00d and prod == 2.250d and quot == Some(2.50d) and diff == 1.50d
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Decimal has `/`: written as the operator or through `Divide.divide`, a
// division of two Decimals is a Decimal, and Decimal meets a `Divide` bound.
// An Int or Float on the other side is the operand mismatch `*` gives.
func TestDecimalDivisionTypechecks(t *testing.T) {
	src := `fn quotient(x: Decimal, y: Decimal) -> Decimal
  x / y
end

fn half<T>(x: T, two: T) -> T where T: Divide<T, T>
  x / two
end

fn f() -> Decimal
  a = 1d / 3d
  b = Divide.divide(1d, 3d)
  c = Decimal.divide(1d, 3d)
  a + b + c + quotient(1d, 2d) + half(3d, 2d)
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestDecimalMixedDivisionRejected(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{"1d / 3", "binary / type mismatch: Decimal vs Int"},
		{"1 / 3d", "binary / type mismatch: Int vs Decimal"},
	} {
		_, errs := checkSourceWithStdlib("fn f() -> Decimal\n  " + tc.expr + "\nend\n")
		found := false
		for _, e := range errs {
			if e.Message == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %v", tc.expr, tc.want, errs)
		}
	}
}

// A Decimal on the right of a type with its own `Divide<Decimal, _>` impl is
// that impl's to answer, not the Decimal division error.
func TestDecimalDivisorOfAUserTypeUsesItsImpl(t *testing.T) {
	src := `struct Money
  amount: Decimal
end

impl Divide<Decimal, Money> for Money
  fn divide(lhs: Money, rhs: Decimal) -> Money
    Money{amount: lhs.amount / rhs}
  end
end

fn f() -> Money
  Money{amount: 10d} / 4d
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Comparisons and equality between two Decimals type-check (Bool result),
// including the scale-insensitive equality 1.50d == 1.5d.
func TestDecimalComparisonsTypecheck(t *testing.T) {
	src := `fn f() -> Bool
  a = 1.50d < 2d
  b = 1.50d == 1.5d
  c = 1.50d >= 1.5d
  a and b and c
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Mixed Int/Decimal arithmetic is rejected (no implicit conversion).
func TestDecimalMixedArithmeticRejected(t *testing.T) {
	src := `fn f() -> Decimal
  1 + 1.50d
end
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Float/Decimal equality is rejected.
func TestDecimalMixedEqualityRejected(t *testing.T) {
	src := `fn f() -> Bool
  1.0 == 1d
end
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// Mixed Int/Decimal comparison is rejected.
func TestDecimalMixedComparisonRejected(t *testing.T) {
	src := `fn f() -> Bool
  1 < 2.50d
end
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "mismatch")
}

// `%` dispatches through `Remainder`: Decimal, Float and Duration have
// impls, and Decimal and Float meet a `Remainder` bound.
func TestRemainderOnTheStdNumbersTypechecks(t *testing.T) {
	src := `import std::duration.Duration

fn rem<T>(a: T, b: T) -> T where T: Remainder<T, T>
  a % b
end

fn f() -> Bool
  d = 5.0d % 2d
  x = 7.5 % 2.0
  quarter = Duration.minutes(40) % Duration.minutes(15)
  d == 1.0d and x == 1.5 and rem(10.5d, 3d) == 1.5d and rem(1.5, 1.0) == 0.5 and quarter == Duration.minutes(10)
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// A type with no `Remainder` impl gets the missing-impl error `/` gives a
// type with no `Divide`, at the operator.
func TestRemainderWithoutAnImplRejected(t *testing.T) {
	src := `type Score Int

fn f(a: Score, b: Score) -> Score
  a % b
end
`
	errs := buildWithStdlibForDerive(src).TypeErrors
	want := "no impl of `Remainder` for `Score` (required via remainder operator `%`)"
	e, ok := deriveDiag(errs, want)
	if !ok {
		t.Fatalf("expected %q, got %v", want, errs)
	}
	if e.Line != 4 || e.Col != 5 {
		t.Fatalf("error at %d:%d, want 4:5", e.Line, e.Col)
	}
}

// Int % Int stays accepted.
func TestIntModuloAccepted(t *testing.T) {
	src := `fn f(a: Int, b: Int) -> Int
  a % b
end
`
	if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
		t.Fatalf("Int %% Int rejected: %v", errs)
	}
}
