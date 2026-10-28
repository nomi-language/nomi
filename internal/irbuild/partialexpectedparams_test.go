package irbuild

import "testing"

// A partial capture typed at its position's parameter types runs: the
// open argument is the concrete list, which the body's call views as the
// Iter the callee's slot declares.
func TestIRPartialCaptureAtExpectedParameterTypes_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    found: Result<List<List<String>>, String> = Ok([["a", "b"], ["c"]])
    io.inspect(Result.map(found, &Iter.flatten(&1)))
    io.inspect(Maybe.map(Some([[1], [2, 3]]), &Iter.flatten(&1)))
    io.inspect([[[1], [2]], [[3]]] |> Iter.map(&Iter.flatten(&1)) |> Iter.to_list())
}
`
	irRunSource(t, src, "Ok([\"a\", \"b\", \"c\"])\nSome([1, 2, 3])\n[[1, 2], [3]]\n")
}
