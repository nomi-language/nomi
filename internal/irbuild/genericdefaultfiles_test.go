package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// genericDefaultShape declares an interface whose default has a type
// parameter of its own.
const genericDefaultShape = `pub interface Shape {
    fn area(s: self) -> Int

    fn pick<T>(s: self, a: T, b: T) -> T {
        if area(s) > 10 { a } else { b }
    }
}
`

// genericDefaultTile implements Shape without writing pick.
const genericDefaultTile = `pub struct Tile {
    side: Int
}

impl Shape for Tile {
    fn area(t: Tile) -> Int {
        t.side * t.side
    }
}
`

// A generic default declared in another file is instantiated per call as
// one declared in the caller's file is (methodinst.go), whichever file the
// implementing block is in. The mirror of the interface could not import
// the default's `T` positions, which have no kind before a call, and
// recorded the function as a non-scalar signature, which withheld it from
// instantiation: "qualified call, Type.method".
func TestGenericIfaceDefault_DeclaredInAnotherFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const calls = `
fn main() {
    t = Tile{side: 4}
    io.print(Tile.pick(t, "big", "small"))
    io.print(Shape.pick(t, 1, 2))
    io.print(Shape.pick(Tile{side: 2}, 1, 2))
}
`
	const want = "big\n1\n2\n"
	t.Run("the impl in the calling file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\n\n"+genericDefaultTile+calls, want,
			map[string]string{"shape.nomi": genericDefaultShape})
	})
	t.Run("the impl in a third file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport tiles.Tile\n"+calls, want,
			map[string]string{
				"shape.nomi": genericDefaultShape,
				"tiles.nomi": "import shape.Shape\n\n" + genericDefaultTile,
			})
	})
}

// The front end accepts the program above, so the test must fail if the
// checker starts to reject it rather than pass vacuously.
func TestGenericIfaceDefault_FrontEndAccepts(t *testing.T) {
	src := genericDefaultShape + genericDefaultTile + `
fn main() {
    _ = Tile.pick(Tile{side: 4}, "big", "small")
    _ = Shape.pick(Tile{side: 4}, 1, 2)
}
`
	if _, err := frontend.New(frontend.Config{}).CheckSource("main", src, frontend.Mode{}); err != nil {
		t.Fatalf("the front end rejects this, so the lowering test above says nothing: %v", err)
	}
}
