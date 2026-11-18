package analysis_test

import (
	"strings"
	"testing"
)

const elementCallbackHead = `fn verse(day: PositiveInt) -> String
    "day ${day}"
end

`

// A callback a std Iter function calls with elements whose range proves
// the callback's parameter is accepted however it is written: a lambda, a
// lambda with the parameter annotated, a partial capture and the function
// passed by name, in a direct call and a pipe stage alike
// (checker.elemProves).
func TestElementCallback_EveryFormTakesWhatTheRangeProves(t *testing.T) {
	for _, call := range []string{
		"Iter.map(1..=n, fn d -> verse(d))",
		"Iter.map(1..=n, fn d: PositiveInt -> verse(d))",
		"Iter.map(1..=n, &verse(&1))",
		"Iter.map(1..=n, verse)",
		"1..=n |> Iter.map(fn d -> verse(d))",
		"1..=n |> Iter.map(fn d: PositiveInt -> verse(d))",
		"1..=n |> Iter.map(&verse(&1))",
		"1..=n |> Iter.map(verse)",
		"1..=n |> Iter.map(f: verse)",
	} {
		src := elementCallbackHead + "fn run(n: Int) -> List<String>\n    " + call + " |> Iter.to_list()\nend\n"
		if errs := typeErrorsOf(t, src); len(errs) > 0 {
			t.Errorf("the front end rejects `%s`:\n%v", call, errs)
		}
	}
}

// Where the elements' range does not prove the parameter, the function by
// name is no function of the elements, as its capture is not.
func TestElementCallback_AnUnprovenRangeIsRejected(t *testing.T) {
	for _, call := range []string{
		"Iter.map([0, 1], verse)",
		"[0, 1] |> Iter.map(verse)",
		"Iter.map(xs, verse)",
	} {
		src := elementCallbackHead + "fn run(xs: List<Int>) -> List<String>\n    " + call + " |> Iter.to_list()\nend\n"
		errs := typeErrorsOf(t, src)
		if len(errs) == 0 {
			t.Errorf("the front end admits `%s`, whose elements may be zero or below", call)
			continue
		}
		if all := strings.Join(errs, "\n"); !strings.Contains(all, "argument 2: expected (Int) -> ") || !strings.Contains(all, ", got (PositiveInt) -> String") {
			t.Errorf("`%s` is rejected with another message:\n%v", call, errs)
		}
	}
}
