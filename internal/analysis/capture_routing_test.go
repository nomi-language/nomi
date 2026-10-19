package analysis_test

import "testing"

// A capture is a lambda written another way, so it routes to a trailing
// callback past a defaulted parameter on sight, as a lambda and a field
// accessor do (spec §5, *Lambdas at Call Sites*): an operator, a partial
// capture (`&String.length(&1)`), a call that is part of a larger
// expression and a struct literal, directly and through a pipe, into a user
// function shaped like the spec's `do_thing` and into `Iter.sort_by`, whose
// `direction` is defaulted before `key`.
func TestCaptureRoutesAsATrailingCallback(t *testing.T) {
	const decls = `
struct Point {
  x: Int
  y: Int
}

fn do_thing(required: Int, option_a: String = "default", f: (Int) -> Int) -> Int {
  f(required) + String.length(option_a)
}

fn make(required: Int, _option_a: String = "default", f: (Int) -> Point) -> Point {
  f(required)
}

fn twice(n: Int, by: Int) -> Int {
  n * by
}
`
	for _, tc := range []struct{ name, body string }{
		{"operator", "do_thing(5, &(&1 * 2))"},
		{"partial", "do_thing(5, &Int.max(&1, 7))"},
		{"partial of a user function", "do_thing(5, &twice(&1, 3))"},
		{"named partial", "do_thing(5, &twice(by: 3, n: &1))"},
		{"partial after a named argument", `do_thing(5, option_a: "x", &Int.max(&1, 7))`},
		{"call in an expression", "do_thing(5, &(Int.max(&1, 7) + 1))"},
		{"piped partial", "5 |> do_thing(&Int.max(&1, 7))"},
		{"struct literal", "make(5, &#Point{x: &1, y: 0}).x"},
		{"sort_by partial", `Iter.sort_by(["ccc", "a"], &String.length(&1)) |> Iter.count()`},
		{"sort_by operator", `Iter.sort_by([3, 1], &(0 - &1)) |> Iter.count()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "\nfn demo() -> Int {\n  " + tc.body + "\n}\n")
			for _, e := range errs {
				t.Errorf("%s: %s", tc.body, e.Message)
			}
		})
	}
}
