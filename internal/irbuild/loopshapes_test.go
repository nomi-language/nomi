package irbuild

import "testing"

// `Iter.loop` lowers a function-valued state (a function, a list of them),
// an `if`-pattern tail with an `else` (value arms, a `break` arm, a
// nested guard), and parameters after the state that take their defaults
// every iteration, a destructuring one among them.
func TestIRLoopShapes_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    m: Maybe<Int> = Some(3)
    n = Iter.loop(0, fn n ->
        if n > 3 then break n
        if Some(v) = m then n + v else n + 1
    end)
    io.inspect(n)
    xs = [5, 6, 7]
    found = Iter.loop(0, fn i ->
        if Some(x) = Iter.get(xs, i) then
            if x == 6 then break i
            i + 1
        else
            break -1
        end
    end)
    io.inspect(found)
    k = Iter.loop(0, fn i ->
        if Some(x) = Iter.get(xs, i) then break x * 10 else i + 1
    end)
    io.inspect(k)
    f = Iter.loop(fn x: Int -> x, fn g ->
        if g(0) > 3 then break g
        fn x: Int -> g(x) + 1
    end)
    io.inspect(f(1))
    fs = Iter.loop([fn x: Int -> x], fn fs: List<(Int) -> Int> ->
        if Iter.count(fs) >= 3 then break fs
        [fn x: Int -> x * 2, ..fs]
    end)
    io.inspect(fs |> Iter.map(fn h -> h(5)) |> Iter.to_list())
    s = Iter.loop(0, fn a, b = 1 ->
        if a > 3 then break a
        a + b
    end)
    io.inspect(s)
    d = Iter.loop(0, fn a, (b, c) = (1, 2) ->
        if a > 3 then break a
        a + b + c
    end)
    io.inspect(d)
end
`
	const want = "6\n" +
		"1\n" +
		"50\n" +
		"5\n" +
		"[10, 10, 5]\n" +
		"4\n" +
		"6\n"
	irRunSource(t, src, want)
}
