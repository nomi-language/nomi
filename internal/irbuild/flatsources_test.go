package irbuild

import "testing"

// `Iter.flat_map` and `Iter.flatten` view an inner part that is a source
// rather than a List or an Iter (a String, Bytes, a Range, a Set, a
// Vector, a Map, a user source) as the Iter it is, and a flat_map callback
// that signals still skips or stops.
func TestIRFlatMapAndFlattenOverInnerSources_Run(t *testing.T) {
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
}

fn main() {
    io.inspect(["ab", "cd"] |> Iter.flat_map(fn s -> s) |> Iter.to_list())
    io.inspect([1, 3] |> Iter.flat_map(fn n -> n..n + 2) |> Iter.to_list())
    io.inspect([1, 3] |> Iter.flat_map(fn n -> %[n]) |> Iter.to_list())
    io.inspect([1, 3] |> Iter.flat_map(fn n -> #[n, n]) |> Iter.to_list())
    io.inspect([1, 3] |> Iter.flat_map(fn n -> %{n => n * 2}) |> Iter.to_list())
    io.inspect([1, 3] |> Iter.flat_map(fn n -> #Pair{a: n, b: n}) |> Iter.count())
    io.inspect([1, 2, 3] |> Iter.flat_map(fn n -> if n == 2 { continue } else { #[n, n] }) |> Iter.to_list())
    io.inspect([1, 2, 3] |> Iter.flat_map(fn n -> if n == 2 { break "zz" } else { "x" }) |> Iter.to_list())
    io.inspect(Iter.flatten(["ab", "cd"]))
    io.inspect(Iter.flatten([1..3, 5..=6]))
    io.inspect(Iter.flatten([%[1], %[2]]))
    io.inspect(Iter.flatten([#[1, 2], #[3]]))
    io.inspect(Iter.flatten(#["ab", "c"]))
    io.inspect(Iter.flatten([String.to_bytes("hi")]))
    io.inspect(Iter.flatten([#Pair{a: 1, b: 2}, #Pair{a: 3, b: 4}]))
}
`
	const want = "[\"a\", \"b\", \"c\", \"d\"]\n" +
		"[1, 2, 3, 4]\n" +
		"[1, 3]\n" +
		"[1, 1, 3, 3]\n" +
		"[#(1, 2), #(3, 6)]\n" +
		"4\n" +
		"[1, 1, 3, 3]\n" +
		"[\"x\", \"z\", \"z\"]\n" +
		"[\"a\", \"b\", \"c\", \"d\"]\n" +
		"[1, 2, 5, 6]\n" +
		"[1, 2]\n" +
		"[1, 2, 3]\n" +
		"[\"a\", \"b\", \"c\"]\n" +
		"[104, 105]\n" +
		"[1, 2, 3, 4]\n"
	irRunSource(t, src, want)
}
