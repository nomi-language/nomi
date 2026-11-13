package analysis_test

import "testing"

// A function has no order. An ordering operator over a tuple holding one
// demands Comparable of each element, and the function's demand used to be
// dropped (it has no nominal name), so the program reached the builder,
// which declined it as not supported yet.
func TestFunctionOrdering_IsRejected(t *testing.T) {
	const want = "no impl of `Comparable` for `(Int) -> Int`; function types cannot implement `Comparable`"
	src := "fn main()\n    f = fn n: Int -> n + 1\n    _ = (f, 1) < (f, 2)\nend\n"
	_, errs := checkSourceWithStdlib(src)
	var got []string
	for _, e := range errs {
		got = append(got, e.Message)
		if e.Message == want {
			if e.Line != 3 || e.Col != 16 {
				t.Errorf("reported at %d:%d, want 3:16", e.Line, e.Col)
			}
			return
		}
	}
	t.Errorf("the checker admits this source or reports something else; want %q, got %v\n%s", want, got, src)
}
