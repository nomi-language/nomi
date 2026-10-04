package irbuild

import (
	"testing"
)

func TestIRScalarOperator_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn left(): Int { io.print("left"); 9 }
fn right(): Int { io.print("right"); 4 }
fn main() {
 io.print(Add.add(left(), right()))
 io.print(Int.subtract(9, 4))
 io.print(Multiply.multiply(3, 4))
 io.print(9 |> Divide.divide(2))
 io.print(Float.add(1.5, 2.5))
 io.print(Divide.divide(1.0, 2.0))
 io.print(Add.add(1.25d, 2.5d))
 io.print(Divide.divide(1d, 4d))
 io.print(String.add("a", "b"))
}
`, "left\nright\n13\n5\n12\n4\n4.0\n0.5\n3.75\n0.25\nab\n")
}

func TestIRScalarOperator_OverflowBlamesCall(t *testing.T) {
	verifyIterFault(t, `import std/io
fn overflow(): Int {
 big = 9223372036854775807
 Add.add(big, 1)
}
fn main() { io.print(overflow()) }
`, "line 4: integer overflow: 9223372036854775807 + 1", "")
}

func TestIRScalarOperator_CheckedFaults(t *testing.T) {
	for _, tc := range []struct{ expression, fault string }{
		{"Subtract.subtract((-9223372036854775807 - 1), 1)", "integer overflow"},
		{"Multiply.multiply(9223372036854775807, 2)", "integer overflow"},
		{"Divide.divide((-9223372036854775807 - 1), -1)", "integer overflow"},
		{"Int.divide(1, 0)", "division by zero"},
		{"Divide.divide(1d, 0d)", "decimal division by zero"},
		{"Divide.divide(1d, 3d)", "non-terminating decimal division"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			verifyIterFault(t, "import std/io\nfn main() {\n io.print("+tc.expression+")\n}\n", "line 3: "+tc.fault, "")
		})
	}
}
