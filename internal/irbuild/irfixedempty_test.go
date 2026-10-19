package irbuild

import "testing"

// An empty literal whose type the checker fixed from where it sits is built
// at that type: a lambda's or a nested fn's `return []` beside a typed
// return, a recursive nested fn's `[]` tail, `break []` in an `Iter.map`
// callback, `break []`, `return []` and a `[]` tail against an `Iter.loop`'s
// `List<Int>` state, a tuple's `[]` compared with another tuple, and a
// generic struct's field.
func TestIREmptyLiteralAtItsFixedType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/io.Captured

fn main() {
    g = fn x: Int -> {
        if x > 0 { return [] }
        if x < 0 { return [x] }
        [x, x]
    }
    io.inspect([g(1), g(-1), g(0)])
    fn none_list(x: Int) -> List<Int> {
        if x > 5 { return [x] }
        []
    }
    io.inspect(none_list(1))
    fn r(n: Int) -> List<Int> {
        if n > 0 {
            io.inspect(r(n - 1))
        }
        []
    }
    io.inspect(r(1))
    ys = [1, 2, 3]
        |> Iter.map(fn x -> {
            if x > 2 { break [] }
            if x > 1 { break [x] }
            [x, x]
        })
        |> Iter.to_list()
    io.inspect(ys)
    a = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break [] }
        [1, ..acc]
    })
    b = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break acc }
        if Iter.count(acc) == 0 { return [] }
        [1, ..acc]
    })
    c = Iter.loop(fn acc: List<Int> = [] -> {
        if Iter.count(acc) == 1 { break acc }
        if Iter.count(acc) > 3 { [] } else { [2, ..acc] }
    })
    io.inspect(#(a, b, c))
    io.print(#(1, []) == #(1, [2]))
    io.print(#(1, []) == #(1, []))
    k: Captured<List<Int>> = #Captured{value: [], output: "", transcript: ""}
    io.inspect(k.value)
}
`
	irRunSource(t, src, "[[], [-1], [0, 0]]\n[]\n[]\n[]\n[[1, 1], [2]]\n#([], [1, 1, 1], [2])\nFalse\nTrue\n[]\n")
}
