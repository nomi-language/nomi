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
	const src = `import std/io

fn main() {
    m: Maybe<Int> = Some(3)
    n = Iter.loop(|n = 0| {
        if n > 3 { break n }
        if Some(v) = m { n + v } else { n + 1 }
    })
    io.inspect(n)
    xs = [5, 6, 7]
    found = Iter.loop(|i = 0| {
        if Some(x) = Iter.get(xs, i) {
            if x == 6 { break i }
            i + 1
        } else {
            break -1
        }
    })
    io.inspect(found)
    k = Iter.loop(|i = 0| {
        if Some(x) = Iter.get(xs, i) { break x * 10 } else { i + 1 }
    })
    io.inspect(k)
    f = Iter.loop(|g = |x: Int| x| {
        if g(0) > 3 { break g }
        |x: Int| g(x) + 1
    })
    io.inspect(f(1))
    fs = Iter.loop(|fs: List<(Int) -> Int> = [|x: Int| x]| {
        if Iter.count(fs) >= 3 { break fs }
        [|x: Int| x * 2, ..fs]
    })
    io.inspect(fs |> Iter.map(|h| h(5)) |> Iter.to_list())
    s = Iter.loop(|a = 0, b = 1| {
        if a > 3 { break a }
        a + b
    })
    io.inspect(s)
    d = Iter.loop(|a = 0, (b, c) = (1, 2)| {
        if a > 3 { break a }
        a + b + c
    })
    io.inspect(d)
}
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
