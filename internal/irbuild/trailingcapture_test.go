package irbuild

import "testing"

// A capture routes to a trailing callback past a defaulted parameter as a
// lambda does (spec §5, *Lambdas at Call Sites*): a partial capture
// (`&String.length(&1)`, `&twice(by: 3, n: &1)`), an operator, a call
// inside a larger expression and a struct literal, into `Iter.sort_by` and
// into a user function shaped like the spec's `do_thing`, directly and
// through a pipe.
func TestVM_CaptureRoutesAsATrailingCallback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

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

fn main() {
    io.inspect(Iter.sort_by(["ccc", "a", "bb"], &String.length(&1)) |> Iter.to_list())
    io.inspect(Iter.sort_by([2, 3, 1], &(0 - &1)) |> Iter.to_list())
    io.inspect(do_thing(5, &(&1 * 2)))
    io.inspect(do_thing(5, &Int.max(&1, 70)))
    io.inspect(do_thing(5, &twice(&1, 3)))
    io.inspect(do_thing(5, &twice(by: 4, n: &1)))
    io.inspect(do_thing(5, option_a: "x", &Int.max(&1, 70)))
    io.inspect(do_thing(5, &(Int.max(&1, 70) + 1)))
    io.inspect(5 |> do_thing(&Int.max(&1, 70)))
    io.inspect(make(5, &#Point{x: &1, y: 0}))
}
`
	irRunSource(t, src, `["a", "bb", "ccc"]
[3, 2, 1]
17
77
22
27
71
78
77
#Point{x: 5, y: 0}
`)
}

// In a test body, an assertion's call that routes a capture past a
// defaulted parameter records each argument against the slot it fills, so
// a failing assertion's rows name the callback as `f`.
func TestVM_TrailingCaptureInATestBody(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `fn do_thing(required: Int, option_a: String = "default", f: (Int) -> Int) -> Int {
    f(required) + String.length(option_a)
}

test "a trailing partial routes past the default" {
    assert do_thing(5, &Int.max(&1, 70)) == 77
    assert Iter.sort_by(["ccc", "a"], &String.length(&1)) == ["a", "ccc"]
}

test "a failing assertion records the routed partial" {
    n = 5
    assert do_thing(n, &Int.max(&1, 70)) == 0
}
`
	irTestBodyVM(t, src, 2)
}
