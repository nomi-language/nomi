package irbuild

import "testing"

// A std generic struct built at an embedded type is rebuilt at the
// embedding enum where that instance is expected: a Captured<Circle> in a
// binding, a Vector's elements, and beside a Captured<Shape> in a literal.
func TestIRStdGenericStructWidened_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/io.Captured

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    c = #Circle{r: 2}
    w: Captured<Shape> = #Captured{value: c, output: "", errors: "", transcript: ""}
    io.inspect(w)
    k: Vector<Captured<Shape>> = #[#Captured{value: c, output: "", errors: "", transcript: ""}]
    io.print(Vector.length(k))
    ka = #Captured{value: c, output: "", errors: "", transcript: ""}
    kb = #Captured{value: Shape.Dot, output: "", errors: "", transcript: ""}
    io.print(Vector.length(#[ka, kb]))
}
`
	irRunSource(t, src, "#Captured{value: #Circle{r: 2}, output: \"\", errors: \"\", transcript: \"\"}\n1\n2\n")
}
