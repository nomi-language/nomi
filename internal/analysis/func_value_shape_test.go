package analysis_test

import (
	"strings"
	"testing"
)

// A function value never fills a generic parameter whose shape is not a
// function, and the reverse (func_value_shape.go). The permissive fallback
// for generic argument mismatches let `Maybe.with_default(f, 0)` through
// with `f: (String) -> Maybe<Int>`, and the program was BLOCKED at run time
// with "a lambda parameter outside the domain".

func TestFuncValueShape_MismatchIsRejected(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want string
	}{
		{"f = |s: String| String.to_int(s)\n  x = Maybe.with_default(f, 0)",
			"argument 1: expected Maybe<T>, got (String) -> Maybe<Int>"},
		{"x: Int = Maybe.with_default(|s: String| String.to_int(s), 0)",
			"argument 1: expected Maybe<T>, got (String) -> Maybe<Int>"},
		{"x = Maybe.map(Some(1), 5)",
			"argument 2: expected (Int) -> U, got Int"},
	} {
		src := "fn main() {\n  " + tc.stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, tc.want) {
				found = true
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

// The mirror: function values where functions go stay accepted. The
// newcomer's shape is among them: a lambda's body runs to the end of its
// expression, so the pipe inside `Iter.map(...)` stays in the lambda, braced
// or not.
func TestFuncValueShape_MatchingShapesAreAccepted(t *testing.T) {
	for _, stmt := range []string{
		"x: List<Int> = [\"1\"] |> Iter.map(|s| String.to_int(s) |> Maybe.with_default(0)) |> Iter.to_list()",
		"x: List<Int> = [\"1\"] |> Iter.map(|s| { String.to_int(s) |> Maybe.with_default(0) }) |> Iter.to_list()",
		"x = Maybe.map(Some(1), |n| n + 1)",
		"f = |n: Int| n + 1\n  x = Maybe.map(Some(1), f)",
		"x = Maybe.with_default(Some(|n: Int| n), |n: Int| n + 1)",
	} {
		src := "fn main() {\n  " + stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
