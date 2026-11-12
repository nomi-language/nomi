package irbuild

import "testing"

// A vector literal where a `Vector<Iter<T>>` is expected holds each element
// as the sequence it enters, including one whose elements widen into an
// embedding enum, and the element-agnostic Vector functions read it.
func TestIRVectorOfIters_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

fn total(it: Iter<Shape>) -> Int
    it
        |> Iter.map(fn s -> case s
            Shape.Circle{r:} -> r
            Shape.Dot -> 100
        end)
        |> Iter.reduce(0, fn acc, n -> acc + n)
end

fn main()
    c = Circle{r: 2}
    v: Vector<Iter<Shape>> = Vector[[c], [Shape.Dot, c]]
    io.print(Vector.length(v))
    io.inspect(v |> Iter.map(fn s -> total(s)) |> Iter.to_list())
    w: Vector<Iter<Int>> = Vector[[1, 2], [3]]
    io.print(Iter.count(w))
    io.inspect(w |> Iter.map(fn s -> Iter.to_list(s)) |> Iter.to_list())
end
`
	irRunSource(t, src, "2\n[2, 102]\n2\n[[1, 2], [3]]\n")
}
