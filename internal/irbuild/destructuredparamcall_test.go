package irbuild

import "testing"

// A destructuring parameter beside named arguments and defaults. A named
// argument after one placed the call (namedQualCall declined any callee
// with one), a default after one reads its names (callDefaults binds them
// from the argument, as the callee's prologue does), another file's accessor
// for such a default was not built (`lib.area((2, 3))` was BLOCKED with no
// position), and a generic function with an annotated destructuring
// parameter was not instantiated at all.
func TestDestructuredParamCalls_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const lib = `pub struct Point {
    x: Int
    y: Int
}

impl Point {
    pub fn scale(Point{x, y}, by: Int = x, plus: Int = 0): Int {
        x * by + y + plus
    }
}

pub fn area((w, h): (Int, Int), scale: Int = w): Int {
    w * h * scale
}
`
	const main = `import {
    std/io
    lib
    lib.Point
}

struct Pt<T> {
    x: T
    y: T
}

impl Pt<T> {
    fn pick<T>(Pt{x, y}: Pt<T>, first: Bool = True, other: T = y): T {
        if first { x } else { other }
    }
}

fn local_area((w, h): (Int, Int), scale: Int = h): Int {
    w * h * scale
}

fn swap<A, B>((a, b): (A, B), keep: Bool = False, fallback: A = a): (B, A) {
    if keep { (b, fallback) } else { (b, a) }
}

fn main() {
    io.print(lib.area((2, 3)))
    io.print(lib.area((2, 3), scale: 2))
    io.print(lib.area(scale: 3, (2, 3)))
    io.print(local_area((2, 3)))
    io.print(local_area(scale: 1, (2, 3)))
    io.print(Point.scale(Point{x: 1, y: 2}, by: 3))
    io.print(Point.scale(Point{x: 2, y: 2}, plus: 3))
    io.inspect(swap(keep: True, (1, "x")))
    io.inspect(swap((1, "x")))
    io.inspect(swap((1, "x"), fallback: 5, keep: True))
    io.inspect(Pt.pick(Pt{x: 1, y: 2}, first: False))
    io.inspect(Pt.pick(Pt{x: "a", y: "b"}, other: "c", first: False))
}
`
	verifyLambdaProgram(t, main, "12\n12\n18\n18\n6\n5\n9\n(\"x\", 1)\n(\"x\", 1)\n(\"x\", 5)\n2\n\"c\"\n",
		map[string]string{"lib.nomi": lib})
}
