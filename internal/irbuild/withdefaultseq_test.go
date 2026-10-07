package irbuild

import "testing"

// `Result.with_default` whose payload the checker typed `Iter<Bool>` from
// the call's position (an argument to `Iter.filter`) takes a list default,
// viewed as the sequence.
func TestIRWithDefaultAtIterPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    f: (List<Bool>) -> Int = |xs| Iter.count(xs)
    io.inspect(f(Iter.filter(Result.with_default(Err("no"), [False, True]), |x| x) |> Iter.to_list()))
    io.inspect(Iter.filter(Maybe.with_default(None, [True]), |x| x) |> Iter.to_list())
}
`
	const want = "1\n" +
		"[True]\n"
	irRunSource(t, src, want)
}
