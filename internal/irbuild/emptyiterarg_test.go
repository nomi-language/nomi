package irbuild

import "testing"

// An empty Set, Vector or List literal passed where an `Iter<T>` is
// declared is built at T and viewed as an empty Iter.
func TestIREmptyCollectionAsIterArgument_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn total(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn names(xs: Iter<String>): String {
    String.join(xs, ",")
}

fn main() {
    io.inspect(total(#{}))
    io.inspect(total(#[]))
    io.inspect(total([]))
    io.inspect(names(#{}))
    io.inspect(total(#{1, 2}))
}
`
	irRunSource(t, src, "0\n0\n0\n\"\"\n2\n")
}
