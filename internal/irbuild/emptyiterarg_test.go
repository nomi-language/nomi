package irbuild

import "testing"

// An empty Set, Vector or List literal passed where an `Iter<T>` is
// declared is built at T and viewed as an empty Iter.
func TestIREmptyCollectionAsIterArgument_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn total(xs: Iter<Int>) -> Int
    Iter.count(xs)
end

fn names(xs: Iter<String>) -> String
    String.join(xs, ",")
end

fn main()
    io.inspect(total(Set[]))
    io.inspect(total(Vector[]))
    io.inspect(total([]))
    io.inspect(names(Set[]))
    io.inspect(total(Set[1, 2]))
end
`
	irRunSource(t, src, "0\n0\n0\n\"\"\n2\n")
}
