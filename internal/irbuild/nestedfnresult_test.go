package irbuild

import "testing"

// A nested fn answers its declared result, as a module function does: an
// embedded struct returned as its enum (from the tail, an early `return`
// and a recursive fn's arms), an empty set, vector or map whose element
// only the return type fixes, and a list of two implementors returned as a
// list of their interface.
func TestIRNestedFnAnswersItsDeclaredResult_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Spot {
    n: Int
}

enum Shape {
    embeds Spot
    Dot
}

fn main() {
    fn spot() -> Shape {
        Spot{n: 3}
    }

    fn pick(n: Int) -> Shape {
        if n > 0 {
            return Spot{n: n}
        }
        Shape.Dot
    }

    fn count(n: Int) -> Shape {
        case n {
            0 -> Spot{n: 0}
            _ -> count(n - 1)
        }
    }

    fn none() -> Set<Int> {
        #{}
    }

    fn empty_vector() -> Vector<Int> {
        #[]
    }

    fn empty_map() -> Map<String, Int> {
        Map.empty()
    }

    fn shown() -> List<Display> {
        [1, "a"]
    }

    io.inspect(spot())
    io.inspect(pick(2))
    io.inspect(pick(0))
    io.inspect(count(2))
    io.inspect(none())
    io.inspect(empty_vector())
    io.inspect(empty_map())
    io.print(shown() |> Iter.map(fn d -> Display.to_string(d)) |> String.join(","))
}
`
	irRunSource(t, src, "Spot{n: 3}\nSpot{n: 2}\nDot\nSpot{n: 0}\n#{}\n#[]\n%{}\n1,a\n")
}
