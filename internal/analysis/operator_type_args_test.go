package analysis_test

import (
	"testing"
)

// TestCallTypeArgs_AnOperatorCallsImplementorIsItsLeftOperand: the type
// argument of an operator interface's function called through the
// interface names its implementor, which the left operand must be, as an
// argument in a `self` position must be the implementor any interface
// function's type argument names. It is one error at the operand, written,
// piped or open in a partial, and an open left operand takes the
// implementor. The type argument was counted and resolved, and then the
// operands chose the impl as though it were not there: `Add.add<Float>(1,
// 2)` was an Int.
func TestCallTypeArgs_AnOperatorCallsImplementorIsItsLeftOperand(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"written", "Add.add<Float>(1, 2)", "argument 1: expected Float, got Int"},
		{"piped", "1 |> Add.add<Float>(2)", "argument 1: expected Float, got Int"},
		{"partial", "f = &Add.add<Float>(&1, 2)\n    f(1.0)", "argument 2: expected Float, got Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn run() -> Float\n    " + tc.body + "\nend\n"
			_, _, errs := checkSourceAsWrittenWithStdlib(src)
			if len(errs) != 1 || errs[0].Message != tc.want {
				t.Fatalf("got %v, want the one error %q", errs, tc.want)
			}
		})
	}
	src := `fn run() -> Float
    f = &Add.add<Float>(&1, 2.0)
    Add.add<Float>(1.5, 2.5) + (1.5 |> Add.add<Float>(2.5)) + f(1.0)
end
`
	if _, _, errs := checkSourceAsWrittenWithStdlib(src); len(errs) > 0 {
		t.Fatalf("the checker rejects calls whose left operand is the implementor: %v", errs)
	}
}
