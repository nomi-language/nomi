package irbuild

import "testing"

// A list, map or vector held as Display's existential renders as
// `Display.to_string` renders it statically: its shape around each
// element's own Display text, a scalar's, a declared type's impl and a
// generic instance's impl alike. A list of an interface's implementers
// is viewed as an `Iter` of the interface without rebuilding it.
func TestIRErasedContainer_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box<T> {
    v: T
}

derive Display for Box<T>

type Meters Int

impl Display for Meters {
    fn to_string(Meters(n)) -> String {
        "${Int.to_string(n)} m"
    }
}

interface Shape {
    fn area(s: self) -> Int
}

struct Circle {
    r: Int
}

impl Shape for Circle {
    fn area(c: Circle) -> Int {
        c.r * 3
    }
}

fn show(d: Display) -> String {
    Display.to_string(d)
}

fn main() {
    a: Display = [1, 2]
    io.print(Display.to_string(a))
    io.print(show(["s", "t"]))
    io.print(show(%{"k" => [True]}))
    io.print(show(#[1.5]))
    io.print(show([Meters(2)]))
    io.print(show([#Box{v: 3}]))
    xs: List<Display> = [[1], [2, 3]]
    io.print(xs |> Iter.map(Display.to_string) |> String.join(";"))
    circle = #Circle{r: 2}
    it: Iter<Shape> = [circle, #Circle{r: 1}]
    io.print(Iter.map(it, Shape.area) |> Iter.to_list())
    nested: List<Iter<Shape>> = [[circle]]
    io.print(Iter.count(nested))
}
`
	const want = "[1, 2]\n" +
		"[s, t]\n" +
		"%{k => [True]}\n" +
		"#[1.5]\n" +
		"[2 m]\n" +
		"[#Box{v: 3}]\n" +
		"[1];[2, 3]\n" +
		"[6, 3]\n" +
		"1\n"
	irRunSource(t, src, want)
}
