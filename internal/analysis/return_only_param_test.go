package analysis_test

import "testing"

// A type parameter named only in the result is solved by the type the call
// is expected to have, so its `where` bound is checked there. Before, the
// parameter stayed an inference variable the binding solved later, and
// `c: Maybe<P> = first()` was accepted for a P with no Variants impl.
func TestReturnOnlyParam_TheExpectedTypeMustMeetTheBound(t *testing.T) {
	src := `struct P {
    n: Int
}

fn first<T>(): Maybe<T> where T: Variants {
    T.variants() |> List.head()
}

fn main() {
    c: Maybe<P> = first()
    _ = c
}
`
	errs := checkWithStdlib(src)
	want := "P does not implement Variants (required by `where T: Variants`)"
	for _, e := range errs {
		if e.Message == want {
			if e.Line != 10 {
				t.Errorf("at line %d, want 10", e.Line)
			}
			return
		}
	}
	t.Fatalf("the front end accepts a P where T: Variants is required; got %v", errs)
}
