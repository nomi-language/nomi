package irbuild

import "testing"

// A Range is a Maybe's or a Result's payload, built, matched, compared and
// inspected.
func TestIRRangeInAPreludeWrapper_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    io.inspect(Some(1..=4))
    case Some(1..4)
        Some(x) -> io.inspect(x |> Iter.to_list())
        None -> io.print("none")
    end
    io.inspect(Some(1..4) == Some(1..4))
    io.inspect([Some(1..=2), None])
    r: Result<Range<Int>, String> = Ok(0..2)
    io.inspect(r)
end
`
	irRunSource(t, src, "Some(1..=4)\n[1, 2, 3]\nTrue\n[Some(1..=2), None]\nOk(0..2)\n")
}

// A Maybe or Result holds a List or Vector of any value: a grid's
// `Vector.get` answer, a vector of lists, sets or maps, and a list of
// vectors of vectors, at every position a wrapper stands.
func TestIRContainerOfCollectionsInAPreludeWrapper_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Board
    rows: Maybe<Vector<Vector<Int>>>
end

fn put(grid: Vector<Vector<Int>>, row: Int, column: Int, value: Int) -> Maybe<Vector<Vector<Int>>>
    cells = Vector.get(grid, row)?
    Some(Vector.set(grid, row, Vector.set(cells, column, value)))
end

fn sets() -> Result<Vector<Set<Int>>, String>
    Ok(Vector[Set[1, 2]])
end

fn lists() -> Result<Int, Vector<List<Int>>>
    Err(Vector[[1], [2, 3]])
end

fn width(maps: Maybe<Vector<Map<String, Int>>>) -> Int
    case maps
        Some(found) -> Vector.length(found)
        None -> 0
    end
end

fn main()
    grid = Vector[Vector[0, 0], Vector[0, 0]]
    io.inspect(put(grid, 1, 0, 7))
    io.inspect(put(grid, 2, 0, 7))
    written = Vector.set(grid, 0, Vector[5])
    io.inspect(written)
    io.inspect(Maybe.map(Vector.get(grid, 0), fn cells -> Vector.set(grid, 1, cells)))
    io.inspect(Board{rows: put(grid, 0, 1, 3)})
    io.inspect(sets())
    io.inspect(lists())
    io.inspect(width(Some(Vector[{"a" => 1}, {"b" => 2}])))
    io.inspect(Map.get({"k" => grid}, "k"))
    io.inspect(Some([Vector[Vector[1]]]) == Some([Vector[Vector[1]]]))
end
`
	irRunSource(t, src, "Some(Vector[Vector[0, 0], Vector[7, 0]])\nNone\nVector[Vector[5], Vector[0, 0]]\nSome(Vector[Vector[0, 0], Vector[0, 0]])\n"+
		"Board{rows: Some(Vector[Vector[0, 3], Vector[0, 0]])}\nOk(Vector[Set[1, 2]])\nErr(Vector[[1], [2, 3]])\n2\n"+
		"Some(Vector[Vector[0, 0], Vector[0, 0]])\nTrue\n")
}
