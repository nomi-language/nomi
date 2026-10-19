package irbuild

import "testing"

// A nested fn's destructuring parameter runs as a lambda's does: a struct
// pattern with no annotation, an annotated tuple in a recursive fn, an
// annotated tuple in a generic one, and a generic one called with named
// arguments whose default reads a destructured name.
func TestIRNestedFnDestructuringParam_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Point {
    x: Int
    y: Int
}

fn main() {
    k = 10
    fn norm(#Point{x, y}) -> Int {
        x * x + y * y + k
    }
    fn count(#(a, b): #(Int, Int)) -> Int {
        if a <= 0 {
            b
        } else {
            count(#(a - 1, b + 1))
        }
    }
    fn pick<T>(#(a, _): #(T, Int)) -> T {
        a
    }
    fn sw<A, B>(#(a, b): #(A, B), keep: Bool = False, fallback: A = a) -> #(B, A) {
        if keep {
            #(b, fallback)
        } else {
            #(b, a)
        }
    }
    io.print(norm(#Point{x: 3, y: 4}))
    io.print(count(#(3, 0)))
    io.print(pick(#("s", 1)))
    io.inspect(sw(keep: True, #(1, "x")))
    io.inspect(sw(#(2, True), fallback: 9, keep: True))
    io.inspect(sw(#(2, True), fallback: 9, keep: False))
}
`
	irRunSource(t, src, "35\n3\ns\n#(\"x\", 1)\n#(True, 9)\n#(True, 2)\n")
}
