package irbuild

import (
	"strings"
	"testing"
)

// A call of a generic function of a shared std module that leaves a
// defaulted parameter out before an operand written positionally after it:
// `NonEmptyList.sort_by(xs, fn n -> n)` past `direction: Direction =
// Direction.Ascending`. The program instantiates such a function for
// itself (stdInstCall), and its default is filled by the accessor the
// program builds in the declaring module at the call's type arguments
// (stdDefaultAccessors). The written operands go to the slots the
// checker's record places them at. They were placed by their index, so
// only a call that wrote a prefix of the parameters lowered, and every
// other declined as "qualified call, Type.method". The same functions
// called with the direction written, with `key:` named, or from a
// program file of the same shape lowered already.
//
// The program declares its own `Direction`, whose first variant is
// `Descending`, so the ascending results show the default is std's.

// stdSkippedDefaultProgram requires the front end to accept src, then runs
// it on the VM and requires want.
func stdSkippedDefaultProgram(t *testing.T, src, want string) {
	t.Helper()
	if _, err := AnalyzeSource("main", src); err != nil {
		t.Fatalf("the front end rejects this, so it tests no lowering: %v", err)
	}
	verifyLambdaProgram(t, src, want)
}

// Each form a positional operand may take past an omitted default: a
// lambda, a capture and a field accessor, in an impl function of NonEmptyList
// and of NonEmptyVector, and as a pipe stage.
func TestStdSkippedDefault_APositionalOperandPastADefault(t *testing.T) {
	stdSkippedDefaultProgram(t, `import std::io
enum Direction
  Descending
  Ascending
end
struct P
  name: String
  age: Int
end
fn main()
  io.print("${NonEmptyList.sort_by([3, 1, 2], fn n -> n)}")
  io.print("${NonEmptyList.sort_by(["bb", "a", "ccc"], &String.length(&1))}")
  io.inspect(NonEmptyList.sort_by([P{name: "a", age: 3}, P{name: "b", age: 1}], .age))
  io.print("${[3, 1, 2] |> NonEmptyList.sort_by(fn n -> 0 - n)}")
  io.print("${NonEmptyVector.sort_by(["bb", "a", "ccc"], fn s -> String.length(s))}")
  io.print("${NonEmptyList.sort_by([3, 1, 2], .Descending, fn n -> n)}")
  io.inspect(Direction.Descending)
end
`, `[1, 2, 3]
[a, bb, ccc]
[P{name: "b", age: 1}, P{name: "a", age: 3}]
[3, 2, 1]
Vector[a, bb, ccc]
[3, 2, 1]
Descending
`)
}

// The same call as an assertion's subject, whose rows name the written
// operands at the slots they fill, and whose failing case reports the
// call's value.
func TestStdSkippedDefault_InAnAssertion(t *testing.T) {
	const src = `test "a positional operand past a generic std function's omitted default"
  assert NonEmptyList.sort_by(["bb", "a", "ccc"], fn s -> String.length(s)) == ["a", "bb", "ccc"]
  assert (["bb", "a"] |> NonEmptyVector.sort_by(&String.length(&1))) == Vector["a", "bb"]
end

test "a failing one reports the call's value"
  assert NonEmptyList.sort_by(["bb", "a"], fn s -> String.length(s)) == ["bb", "a"]
end
`
	if _, err := AnalyzeSource("main", src); err != nil {
		t.Fatalf("the front end rejects this, so it tests no lowering: %v", err)
	}
	out := irTestBodyVM(t, src, 2)
	if !strings.Contains(out, "= [\"a\", \"bb\"]") {
		t.Errorf("the failing case does not report the call's value:\n%s", out)
	}
}
