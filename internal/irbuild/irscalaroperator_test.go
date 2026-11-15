package irbuild

import (
	"testing"
)

func TestIRScalarOperator_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn left() -> Int
    io.print("left")
    9
end
fn right() -> Int
    io.print("right")
    4
end
fn main()
 io.print(Add.add(left(), right()))
 io.print(Int.subtract(9, 4))
 io.print(Multiply.multiply(3, 4))
 io.print(9 |> Divide.divide(2))
 io.print(Float.add(1.5, 2.5))
 io.print(Divide.divide(1.0, 2.0))
 io.print(Add.add(1.25d, 2.5d))
 io.print(Subtract.subtract(1d, 0.75d))
 io.print(String.add("a", "b"))
end
`, "left\nright\n13\n5\n12\n4\n4.0\n0.5\n3.75\n0.25\nab\n")
}

// The written-out impls are exact past 64 bits, as the operators are.
func TestIRScalarOperator_ExactPast64Bits(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
 io.print(Add.add(9223372036854775807, 1))
 io.print(Subtract.subtract((-9223372036854775807 - 1), 1))
 io.print(Multiply.multiply(9223372036854775807, 2))
 io.print(Divide.divide((-9223372036854775807 - 1), -1))
end
`, "9223372036854775808\n-9223372036854775809\n18446744073709551614\n9223372036854775808\n")
}
