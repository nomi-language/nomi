package irbuild

import "testing"

// A source whose elements are of an embedded type enters an `Iter` of the
// embedding enum: an annotated binding, a parameter, a distinct tuple's
// payload, a field default, a spread, `Struct.update`, a Vector and a list
// variable. Each element is widened as it is read, and a `Maybe<Circle>`
// element is rebuilt as a `Maybe<Shape>`.
func TestIREmbeddedSourceAsIterOfItsEnum_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

type P #(Iter<Shape>, Int)

struct H {
    it: Iter<Shape> = [Circle{r: 1}]
    n: Int = 0
}

fn total(it: Iter<Shape>) -> Int {
    it
        |> Iter.map(fn s -> case s {
            Shape.Circle{r} -> r
            Shape.Dot -> 100
        })
        |> Iter.reduce(fn acc = 0, n -> acc + n)
}

fn main() {
    c = Circle{r: 2}
    it: Iter<Shape> = [c, Shape.Dot]
    io.print(total(it))
    xs = [c, c]
    io.print(total(xs))
    io.print(total(#[c]))
    P(pi, pn) = P([c], 1)
    io.print(total(pi) + pn)
    h = H{}
    io.print(total(h.it))
    h2 = {..h, it: [c, c, c]}
    io.print(total(h2.it))
    h3 = Struct.update(updates: {it: [c]}, original: h)
    io.print(total(h3.it))
    m: Iter<Maybe<Shape>> = [Some(c), None]
    io.print(Iter.count(m))
}
`
	irRunSource(t, src, "102\n4\n2\n3\n1\n6\n2\n2\n")
}
