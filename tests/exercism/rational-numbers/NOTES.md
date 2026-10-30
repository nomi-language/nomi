# Rational Numbers

Subject: Numbers & Math

Implement rational numbers: the four arithmetic operations, absolute value, integer powers, a real number raised to a rational power, and reduction to lowest terms.

Adapted from Exercism problem-specifications (MIT), exercises/rational-numbers.

## Choices

- `Rational` is an opaque struct that is always in lowest terms with a positive denominator. `Rational.new` is the only way to make one, so `derive Equatable` compares two rationals by their fields, and `1/2 == 2/4` holds because both are stored as `1/2`.
- `Rational.new` reduces by dividing both parts by their greatest common divisor times the denominator's sign, which also moves a minus sign to the numerator. A zero denominator makes that divisor zero, so it traps as `/` does: the denominator is a divisor the caller chose (style §28). The canonical data has no zero denominator.
- The four operations are `Add`, `Subtract`, `Multiply` and `Divide` impls, so the tests write `a + b` and `a / b`. Each builds its result with `Rational.new`, which reduces it. The impl functions name their parameters `lhs` and `rhs`, as the interfaces do; the checker requires the interface's names.
- `Rational.pow` raises the reciprocal for a negative exponent, with `Int.pow` on each part. `Rational.raise` raises a `Float` to a rational power through `Float.pow` and the exponent as a `Float`.
- `Rational.numerator` and `Rational.denominator` read the parts, since the struct is opaque. The reduction cases check the parts, which is what "reduced" means; the other cases compare rationals.
- The real-power cases compare with `Float.equal_within(actual, expected, 1e-12)`: eight to the power 4/3 is `15.999999999999998`, not the canonical `16.0`, and rounding to a fixed number of places does not fit the canonical `0.3333333333333333`.
- The tests write each rational as the canonical `#(numerator, denominator)` pair and build it with a `rational` helper, so every row reads like the canonical data. Each canonical group is one `subtests` table.

## Friction

None.
