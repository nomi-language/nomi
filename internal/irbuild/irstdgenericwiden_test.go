package irbuild

import (
	"strings"
	"testing"
)

// A std generic struct's literal holding an embedded type, where the
// embedding enum's instance is expected, is built at that instance, its
// field widened: in a binding, a Vector's elements, and after a
// Captured<Shape> in a literal. A std struct's type parameters are
// invariant (spec §5 *Covariant conversion*), so a Captured<Circle> value
// already built never converts to a Captured<Shape>: `Vector[ka, kb]` for
// one of each is refused, and only a literal checked at the wider type
// widens.
func TestIRStdGenericStructWidened_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io
import std::io.Captured

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

fn main()
    c = Circle{r: 2}
    w: Captured<Shape> = Captured{value: c, output: "", errors: "", transcript: ""}
    io.inspect(w)
    k: Vector<Captured<Shape>> = Vector[Captured{value: c, output: "", errors: "", transcript: ""}]
    io.print(Vector.length(k))
    kb = Captured{value: Shape.Dot, output: "", errors: "", transcript: ""}
    io.print(Vector.length(Vector[kb, Captured{value: c, output: "", errors: "", transcript: ""}]))
end
`
	irRunSource(t, src, "Captured{value: Circle{r: 2}, output: \"\", errors: \"\", transcript: \"\"}\n1\n2\n")

	built := strings.Replace(src, `Vector[kb, Captured{value: c, output: "", errors: "", transcript: ""}]`,
		`Vector[Captured{value: c, output: "", errors: "", transcript: ""}, kb]`, 1)
	_, err := AnalyzeSource("entry.nomi", built)
	if err == nil {
		t.Fatal("the front end admits a built Captured<Circle> beside a Captured<Shape>, though a std struct's type parameters are invariant")
	}
	if want := "vector element type mismatch: expected Captured<Circle>, got Captured<Shape>"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the front end rejects a built Captured<Circle> beside a Captured<Shape> with %v, want %q", err, want)
	}
}
