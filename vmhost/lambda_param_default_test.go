package vmhost_test

import "testing"

// A lambda parameter that destructures and has a default runs in every place
// a lambda takes one: a lambda bound to a name and called with and without
// the argument, a later parameter whose default reads an earlier destructured
// one, an `Iter.reduce` seed and an `Iter.loop` state. Each shape covers a
// tuple, a struct and a nested pattern. The default's supplier is built as a
// plain parameter's is, and the pattern binds from whichever value arrives.
func TestLambdaParamDefault_DestructuringPatternsRun(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Point {
    x: Int
    y: Int
}

fn main() {
    f = fn #(a, b): #(Int, Int) = #(1, 2) -> a + b
    io.inspect(f())
    io.inspect(f(#(10, 20)))
    g = fn #Point{x, y} = #Point{x: 3, y: 4} -> x * y
    io.inspect(g())
    io.inspect(g(#Point{x: 5, y: 6}))
    h = fn #(a, #(b, c)): #(Int, #(Int, Int)) = #(1, #(2, 3)) -> a + b * c
    io.inspect(h())
    io.inspect(h(#(2, #(3, 4))))
    n = fn #(p, #Point{x, y}): #(String, Point) = #("o", #Point{x: 0, y: 0}) -> "${p}${x}${y}"
    io.inspect(n())
    io.inspect(n(#("q", #Point{x: 1, y: 2})))
    k = fn n: Int, #(lo, hi): #(Int, Int) = #(n, n + 1) -> lo + hi
    io.inspect(k(5))
    io.inspect(k(5, #(0, 1)))
    s = fn #(a, b): #(Int, Int), #(c, d): #(Int, Int) = #(b, a) -> a * 1000 + b * 100 + c * 10 + d
    io.inspect(s(#(1, 2)))
    io.inspect(s(#(1, 2), #(3, 4)))
    r = [1, 2, 3, 4] |> Iter.reduce(fn #(sum, count) = #(0, 0), x -> #(sum + x, count + 1))
    io.inspect(r)
    q = [1, 2, 3] |> Iter.reduce(fn #Point{x, y} = #Point{x: 0, y: 1}, v -> #Point{x: x + v, y: y * (v + 1)})
    io.inspect(q)
    w = [1, 2] |> Iter.reduce(fn #(s, #(lo, hi)) = #(0, #(100, 0)), v -> #(s + v, #(if v < lo { v } else { lo }, if v > hi { v } else { hi })))
    io.inspect(w)
    l = Iter.loop(fn #(n, acc): #(Int, Int) = #(5, 1) -> {
        if n == 0 { break #(n, acc) }
        #(n - 1, acc * n)
    })
    io.inspect(l)
    m = Iter.loop(fn #Point{x, y} = #Point{x: 0, y: 0} -> {
        if x == 3 { break #Point{x, y} }
        #Point{x: x + 1, y: y + x}
    })
    io.inspect(m)
    o = Iter.loop(fn #(i, #(a, b)) = #(0, #(0, 1)) -> {
        if i == 10 { break #(i, #(a, b)) }
        #(i + 1, #(b, a + b))
    })
    io.inspect(o)
}
`)
	want := `3
30
12
30
7
14
"o00"
"q12"
11
1
1221
1234
#(10, 4)
#Point{x: 6, y: 24}
#(3, #(1, 2))
#(0, 120)
#Point{x: 3, y: 3}
#(10, #(55, 89))
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
