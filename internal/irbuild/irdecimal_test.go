package irbuild

import (
	"testing"
)

func TestIRDecimal_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn sum(a: Decimal, b: Decimal) -> Decimal
    a + b
end
fn divide(a: Decimal, b: NonZeroDecimal) -> Decimal
    Decimal.divide_exact(a, b) |> Maybe.with_default(0d)
end
fn main()
  io.print(sum(0.1d, 0.2d) == 0.3d)
  io.print(1.50d == 1.5d)
  io.print(1.50d != 1.5d)
  io.inspect(sum(19.99d, 5.00d) + 2.50d + 0.99d)
  io.inspect(divide(1.0d, 8d))
  io.inspect(-1.50d)
  io.inspect(999999999999999999999999999999d + 1d)
  io.inspect(1.25d * 4d - 0.5d)
  io.print("amount ${1.50d}")
end
`, "True\nTrue\nFalse\n28.48d\n0.125d\n-1.50d\n1000000000000000000000000000000d\n4.50d\namount 1.50\n")
}

func TestIRDecimal_EffectsAndBranchValues(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(label: String, d: Decimal) -> Decimal
    io.print(label)
    d
end
fn choose(b: Bool) -> Decimal
    if b then 1.50d else 2.25d
end
fn main()
  answer = mark("left", choose(True)) + mark("right", choose(False))
  io.print(answer)
  io.print(mark("a", 1.5d) == mark("b", 1.50d))
  f = fn d: Decimal -> -d
  io.inspect(f(answer))
end
`, "left\nright\n3.75\na\nb\nTrue\n-3.75d\n")
}

// `/` on Decimal rounds to 34 significant digits and keeps an exact
// quotient's smallest scale, through the operator, `Divide.divide` and
// `Decimal.divide` with two arguments.
func TestIRDecimal_Division(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn quotient(a: Decimal, b: NonZeroDecimal) -> Decimal
    a / b
end
fn half<T, D>(x: T, two: D) -> T where T: Divide<D, T>
    x / two
end
fn main()
  io.inspect(quotient(10d, 3d))
  io.inspect(quotient(10.00d, 4d))
  io.inspect(Divide.divide(1d, 4d))
  io.inspect(Decimal.divide(6d, 3d))
  io.inspect(half(-7d, NonZeroDecimal(2d)))
end
`, "3.333333333333333333333333333333333d\n2.5d\n0.25d\n2d\n-3.5d\n")
}
