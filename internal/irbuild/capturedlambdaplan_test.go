package irbuild

import "testing"

// A lambda captured by another closure keeps its defaults and its
// parameter names: an omitted defaulted argument calls the lambda's own
// supplier, captured beside it, once per call; named arguments place by
// its names; and a partial application sees its parameters. Each declined
// with the callee's plan out of sight, at any depth of capture.
func TestCapturedLambdaPlan_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

fn noisy(n: Int) -> Int {
    io.print("default ${n}")
    n
}

fn main() {
    base = 10
    f = fn x: Int, y: Int = noisy(base) + x -> x + y
    g = fn -> f(1)
    io.print(g())
    io.print(g())
    h = fn -> f(y: 1, x: 5)
    io.print(h())
    m = fn x: Int, y: Int = 100 -> x - y
    p = fn -> &m(2, &1)
    q = p()
    io.print(q(3))
    deep = fn -> {
        inner = fn -> f(4)
        inner()
    }
    io.print(deep())
    k = fn a: Int, b: Int = 7 -> a * b
    named = fn n: Int -> k(b: n, a: 2)
    io.inspect([1, 2] |> Iter.map(named) |> Iter.to_list())
    io.inspect([1, 2] |> Iter.map(fn n: Int -> k(n)) |> Iter.to_list())
}
`, "default 10\n12\ndefault 10\n12\n6\n-1\ndefault 10\n18\n[2, 4]\n[7, 14]\n")
}
