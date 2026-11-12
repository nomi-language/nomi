package irbuild

import "testing"

// A type argument nothing determines and no value has is filled (spec,
// "Determined type arguments"), around an empty literal too: a generic
// struct's field, a list's element and a map's value.
func TestIRUnsolvedTypeArgumentAroundAnEmptyLiteral_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Gbox<A>
    v: A
end

fn main()
    io.inspect(Gbox{v: []})
    io.inspect(Gbox{v: Set[]})
    io.inspect(Gbox{v: {}})
    io.inspect(Gbox{v: [[]]})
    io.inspect([Set[]])
    io.inspect([Vector[]])
    io.inspect(Map.get({"k" => []}, "k"))
end
`
	irRunSource(t, src, "Gbox{v: []}\nGbox{v: Set[]}\nGbox{v: {}}\nGbox{v: [[]]}\n[Set[]]\n[Vector[]]\nSome([])\n")
}
