package irbuild

import "testing"

// A list spread's tail is a List (the checker refuses an Iter there), and
// its elements may be any values a list holds: Iters, functions, tuples.
func TestIRListSpreadTailOfAnyElement_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    lines = [1, 2] |> Iter.map(fn x -> x * 10) |> Iter.to_list()
    io.inspect([0, ..lines])
    iters = [[1] |> Iter.map(fn x -> x)]
    more = [[2, 3] |> Iter.map(fn x -> x), ..iters]
    io.inspect(more |> Iter.map(Iter.count) |> Iter.to_list())
    fs = [fn x: Int -> x + 1]
    gs = [fn x: Int -> x * 2, ..fs]
    io.inspect(gs |> Iter.map(fn f -> f(3)) |> Iter.to_list())
}
`
	irRunSource(t, src, "[0, 10, 20]\n[2, 1]\n[6, 4]\n")
}
