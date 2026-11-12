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
	const src = `import std::io

struct Spot
    n: Int
end

enum Shape
    embeds Spot
    Dot
end

fn main()
    fn spot() -> Shape
        Spot{n: 3}
    end

    fn pick(n: Int) -> Shape
        if n > 0 then
            return Spot{n:}
        end
        Shape.Dot
    end

    fn count(n: Int) -> Shape
        case n
            0 -> Spot{n: 0}
            _ -> count(n - 1)
        end
    end

    fn none() -> Set<Int>
        Set[]
    end

    fn empty_vector() -> Vector<Int>
        Vector[]
    end

    fn empty_map() -> Map<String, Int>
        {}
    end

    fn shown() -> List<Display>
        [1, "a"]
    end

    io.inspect(spot())
    io.inspect(pick(2))
    io.inspect(pick(0))
    io.inspect(count(2))
    io.inspect(none())
    io.inspect(empty_vector())
    io.inspect(empty_map())
    io.print(shown() |> Iter.map(fn d -> Display.to_string(d)) |> String.join(","))
end
`
	irRunSource(t, src, "Spot{n: 3}\nSpot{n: 2}\nDot\nSpot{n: 0}\nSet[]\nVector[]\n{}\n1,a\n")
}
