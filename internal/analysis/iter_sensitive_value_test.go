package analysis_test

import (
	"strings"
	"testing"
)

const iterSensitiveValueError = "'stop' uses break/continue (directly or transitively), so it can only be " +
	"called from an iter-callback position or passed directly as an iteration function's callback, not used as a value here"

const iterSensitiveStop = `fn stop(x: Int): Int {
    if x > 2 {
        break
    }
    x
}

fn apply(f: (Int) -> Int, x: Int): Int {
    f(x)
}
`

// An iter-sensitive function named as a value anywhere but directly as an
// iteration function's callback is rejected: its calls through the value
// are call sites the analysis cannot see, so its break or continue could
// run with no iteration to control. `f = stop` used to pass the checker and
// stop the program with "not supported yet".
func TestIterSensitiveValue_IsRejectedOutsideACallbackSlot(t *testing.T) {
	for _, body := range []string{
		"f = stop\n    _ = [1] |> Iter.map(f) |> Iter.to_list()",
		"_ = [1] |> Iter.map(stop) |> Iter.to_list()\n    _ = apply(stop, 1)",
		"_ = [1] |> Iter.map(stop) |> Iter.to_list()\n    fs = [stop]\n    _ = fs",
	} {
		src := iterSensitiveStop + "\nfn main() {\n    " + body + "\n}\n"
		_, errs := checkSourceWithStdlib(src)
		var got []string
		found := false
		for _, e := range errs {
			got = append(got, e.Message)
			found = found || e.Message == iterSensitiveValueError
		}
		if !found {
			t.Errorf("the checker admits this or reports something else; got %v\n%s", got, src)
		}
	}
}

// Passing it directly as the callback, piped or not, stays legal.
func TestIterSensitiveValue_AdmitsADirectCallback(t *testing.T) {
	src := iterSensitiveStop + `
fn main() {
    _ = [1, 2, 3] |> Iter.map(stop) |> Iter.to_list()
    _ = Iter.map([1], stop) |> Iter.to_list()
    _ = [1] |> Iter.map(|x| stop(x)) |> Iter.to_list()
    _ = apply(|x| x, 1)
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "break/continue") {
			t.Errorf("rejected: %s", e.Message)
		}
	}
}
