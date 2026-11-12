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
	const src = `import std::io
import std::io.Captured

fn main()
    g = fn x: Int ->
        if x > 0 then return []
        if x < 0 then return [x]
        [x, x]
    end
    io.inspect([g(1), g(-1), g(0)])
    fn none_list(x: Int) -> List<Int>
        if x > 5 then return [x]
        []
    end
    io.inspect(none_list(1))
    fn r(n: Int) -> List<Int>
        if n > 0 then
            io.inspect(r(n - 1))
        end
        []
    end
    io.inspect(r(1))
    ys = [1, 2, 3]
        |> Iter.map(fn x ->
            if x > 2 then break []
            if x > 1 then break [x]
            [x, x]
        end)
        |> Iter.to_list()
    io.inspect(ys)
    a = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break []
        [1, ..acc]
    end)
    b = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break acc
        if Iter.count(acc) == 0 then return []
        [1, ..acc]
    end)
    c = Iter.loop([], fn acc: List<Int> ->
        if Iter.count(acc) == 1 then break acc
        if Iter.count(acc) > 3 then [] else [2, ..acc]
    end)
    io.inspect((a, b, c))
    io.print((1, []) == (1, [2]))
    io.print((1, []) == (1, []))
    k: Captured<List<Int>> = Captured{value: [], output: "", errors: "", transcript: ""}
    io.inspect(k.value)
end
`
	irRunSource(t, src, "[[], [-1], [0, 0]]\n[]\n[]\n[]\n[[1, 1], [2]]\n([], [1, 1, 1], [2])\nFalse\nTrue\n[]\n")
}
