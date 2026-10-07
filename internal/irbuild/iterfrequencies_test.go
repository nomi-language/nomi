package irbuild

import "testing"

// `Iter.frequencies` runs over a list, a String, a lazy pipeline, struct and
// tuple elements, an empty list, in a pipe, and as a function value. It is
// instantiated from its std body, since iterCall does not lower it itself.
func TestIRIterFrequencies_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Pt {
    x: Int
    y: Int
}

fn main() {
    io.print(Iter.frequencies(["a", "b", "a"]))
    io.print(Iter.frequencies("banana"))
    io.print([3, 1, 3, 3] |> Iter.frequencies())
    io.print(Iter.frequencies(1..=5 |> Iter.map(|n| n % 2)))
    io.inspect(Iter.frequencies([Pt{x: 1, y: 2}, Pt{x: 1, y: 2}]))
    io.print(Iter.frequencies([(1, "a"), (1, "a"), (2, "b")]))
    empty: List<Int> = []
    io.print(Iter.frequencies(empty))
    io.print([["x"], ["y", "y"]] |> Iter.map(Iter.frequencies) |> Iter.to_list())
}
`
	const want = "{a => 2, b => 1}\n" +
		"{b => 1, a => 3, n => 2}\n" +
		"{3 => 3, 1 => 1}\n" +
		"{1 => 3, 0 => 2}\n" +
		"{Pt{x: 1, y: 2} => 2}\n" +
		"{(1, a) => 2, (2, b) => 1}\n" +
		"{=>}\n" +
		"[{x => 1}, {y => 2}]\n"
	irRunSource(t, src, want)
}
