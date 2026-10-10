package irbuild

import "testing"

// A destructuring state binds the pattern's names from the state at the head
// of each iteration. A bare `break` answers the whole state.
func TestIRLoop_DestructuredState(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
    x: Int
    y: Int
}

struct Walk {
    at: Point
    steps: Int
}

fn factorial_loop(input: Int): Int {
    (_, acc) = Iter.loop(|(n, acc): (Int, Int) = (input, 1)| {
        if n <= 1 { break (n, acc) }
        (n - 1, n * acc)
    })
    acc
}

fn main() {
    io.inspect(factorial_loop(5))
    // A bare break answers the state.
    io.inspect(Iter.loop(|(a, b) = (0, 1)| {
        if a > 20 { break }
        (b, a + b)
    }))
    // Nested tuples, with a discarded part.
    io.inspect(Iter.loop(|((lo, hi), _, n) = ((0, 100), "x", 0)| {
        if hi - lo <= 1 { break ((lo, hi), "done", n) }
        mid = (lo + hi) / 2
        if mid * mid > 50 { return ((lo, mid), "low", n + 1) }
        ((mid, hi), "high", n + 1)
    }))
    // A named struct, and an anonymous pattern over it.
    io.inspect(Iter.loop(|Point{x, y} = Point{x: 0, y: 10}| {
        if x >= y { break }
        Point{x: x + 3, y: y - 1}
    }))
    io.inspect(Iter.loop(|{x, y}: Point = Point{x: 1, y: 1}| {
        if x > 30 { break Point{x, y: y + 100} }
        Point{x: x * 2, y}
    }))
    // A struct nested in a struct, with an as-pattern.
    io.inspect(Iter.loop(|Walk{at: Point{x, y} as here, steps} = Walk{at: Point{x: 0, y: 0}, steps: 0}| {
        case steps {
            3 -> break
            _ -> Walk{at: Point{x: here.x + 1, y: y + x}, steps: steps + 1}
        }
    }))
    // In a lambda, with a continue-free body ending in a case.
    sum_to = |limit: Int| Iter.loop(|(i, total) = (0, 0)| {
        case i > limit {
            True -> break
            False -> (i + 1, total + i)
        }
    })
    io.inspect(sum_to(4))
}
`, "120\n(21, 34)\n((7, 8), \"done\", 7)\nPoint{x: 9, y: 7}\nPoint{x: 32, y: 101}\nWalk{at: Point{x: 3, y: 3}, steps: 3}\n(5, 10)\n")
}
