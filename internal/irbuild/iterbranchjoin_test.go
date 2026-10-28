package irbuild

import "testing"

// An `if` or `case` whose branches are different Iters, joined by a written
// `Iter<T>`, views each branch as that Iter and runs.
func TestIRIterBranchJoin_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn in_order(values: Range<Int>, descending: Bool) -> Iter<Int> {
    if descending {
        Iter.reverse(values)
    } else {
        values
    }
}

fn total(xs: Iter<Int>) -> Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(in_order(1..4, True) |> Iter.to_list())
    io.inspect(in_order(1..4, False) |> Iter.to_list())
    pick = 2
    b: Iter<Int> = case pick {
        1 -> 1..=3
        2 -> %[9]
        _ -> [7, 7] |> Iter.map(fn x -> x + 1)
    }
    io.inspect(Iter.to_list(b))
    io.inspect(total(if pick == 2 { #[1, 2] } else { [1] }))
    io.inspect(if pick == 2 { 1..4 } else { [7] } |> Iter.count())
    io.inspect(case pick {
        2 -> [5, 6]
        _ -> 1..3
    } |> Iter.map(fn x -> x * 10) |> Iter.to_list())
}
`
	irRunSource(t, src, "[3, 2, 1]\n[1, 2, 3]\n[9]\n2\n3\n[50, 60]\n")
}
