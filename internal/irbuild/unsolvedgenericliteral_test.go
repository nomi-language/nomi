package irbuild

import "testing"

// A type argument nothing determines and no value has is filled (spec,
// "Determined type arguments"), around an empty literal too: a generic
// struct's field, a list's element and a map's value.
func TestIRUnsolvedTypeArgumentAroundAnEmptyLiteral_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Gbox<A> {
    v: A
}

fn main() {
    io.inspect(Gbox{v: []})
    io.inspect(Gbox{v: #{}})
    io.inspect(Gbox{v: Map.empty()})
    io.inspect(Gbox{v: [[]]})
    io.inspect([#{}])
    io.inspect([#[]])
    io.inspect(Map.get(%{"k" => []}, "k"))
}
`
	irRunSource(t, src, "Gbox{v: []}\nGbox{v: #{}}\nGbox{v: %{}}\nGbox{v: [[]]}\n[#{}]\n[#[]]\nSome([])\n")
}
