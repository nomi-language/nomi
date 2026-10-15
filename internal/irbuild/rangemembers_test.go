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
    io.inspect(Iter.to_map([(1..2, "a"), (3..=4, "b")]) |> Map.get(3..=4))
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
