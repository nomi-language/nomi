package irbuild

import "testing"

// A user `Iter` source whose impl writes its own `known_count` is a source
// like one that inherits the default: it is viewed in a pipeline, passed
// where an `Iter<Int>` is declared and zipped. `Iter.known_count` answers
// the written override and `Iter.count` consults it first, as std's
// `count` does, so an override that does not match the walk is what
// `count` answers. A stage after the source drops the count.
func TestIRUserSourceKnownCount_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Pair {
    a: Int
    b: Int
}

impl Iter for Pair {
    fn each_while(p: Pair, yield: (Int) -> Bool) -> Bool {
        if yield(p.a) { yield(p.b) } else { False }
    }

    fn known_count(_p: Pair) -> Maybe<Int> {
        io.print("counted")
        Some(7)
    }
}

fn total(xs: Iter<Int>) -> Int {
    Iter.count(xs)
}

fn known(xs: Iter<Int>) -> Maybe<Int> {
    Iter.known_count(xs)
}

fn main() {
    p = Pair{a: 1, b: 2}
    io.inspect(total(p))
    io.inspect(known(p))
    io.inspect(Iter.known_count(p))
    io.inspect(p |> Iter.map(fn x -> x + 1) |> Iter.to_list())
    io.inspect(p |> Iter.map(fn x -> x + 1) |> Iter.known_count())
    io.inspect(Iter.zip([5, 6], p) |> Iter.to_list())
}
`
	const want = "counted\n7\n" +
		"counted\nSome(7)\n" +
		"counted\nSome(7)\n" +
		"[2, 3]\n" +
		"None\n" +
		"[#(5, 1), #(6, 2)]\n"
	irRunSource(t, src, want)
}
