package irbuild

import "testing"

// A Range is a Set element and a Map key, hashed and compared structurally,
// and a Range over Bool builds.
func TestIRRangeAsKeyAndOverBool_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    io.inspect(Iter.to_set([1..2, 3..4, 1..2]))
    io.inspect(Iter.to_map([#(1..2, "a"), #(3..=4, "b")]) |> Map.get(3..=4))
    io.inspect(Range.contains?(False..True, True))
    io.inspect(Range.contains?(False..=True, True))
    io.inspect(False..=True)
}
`
	const want = "#{1..2, 3..4}\n" +
		"Some(\"b\")\n" +
		"False\n" +
		"True\n" +
		"False..=True\n"
	irRunSource(t, src, want)
}

// A Range's own members named by the type run: `Range.to_string` and
// `Range.inspect` (std's Debug for a Range is its Display), and the Iter
// impl's `known_count` and `each_while`.
func TestIRRangeMembers_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    io.print(Range.to_string(1..4))
    io.print(Range.inspect(1..=4))
    io.print(Range.to_string(1.5..2.5))
    io.inspect(Range.known_count(1..4))
    io.inspect(Range.known_count(Range.from(1)))
    io.inspect(Range.each_while(1..=4, fn n -> n < 3))
}
`
	const want = "1..4\n" +
		"1..=4\n" +
		"1.5..2.5\n" +
		"Some(3)\n" +
		"None\n" +
		"False\n"
	irRunSource(t, src, want)
}
