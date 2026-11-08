package irbuild

import "testing"

// A parameter default of an interface default is Nomi written in the
// interface's file, so a call that leaves it out fills it there: here
// `default_prefix` and `base_times` are private to shape.nomi, and main.nomi
// declares a `default_prefix` of its own.
// The call used to fill the default at the call site under shape.nomi's
// names, so a default naming a private function there withheld the whole
// default from Square's block as `sibling file interface default`, and an
// erased call in main.nomi (`show`) called main.nomi's `default_prefix`.
func TestIfaceDefault_ParamDefaultsFilledInTheDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const shape = `pub interface Shape {
    fn area(s: self) -> Int

    fn describe(s: self, prefix: String = default_prefix(), times: Int = base_times) -> String {
        "${prefix} ${area(s)} x${times}"
    }
}

fn default_prefix() -> String {
    "shape"
}

once base_times: Int = 1

pub fn all(shapes: List<Shape>) -> List<String> {
    shapes |> Iter.map(&Shape.describe(&1)) |> Iter.to_list()
}
`
	const square = `pub struct Square {
    side: Int
}

impl Shape for Square {
    fn area(s: Square) -> Int {
        s.side * s.side
    }
}
`
	const calls = `fn default_prefix() -> String {
    "main"
}

fn show(s: Shape) -> String {
    Shape.describe(s)
}

fn main() {
    sq = Square{side: 3}
    io.print(Square.describe(sq))
    io.print(Square.describe(sq, "box"))
    io.print(Shape.describe(sq))
    io.print(Square.describe(sq, times: 2))
    io.print(all([sq]))
    io.print(show(sq))
}
`
	const want = "shape 9 x1\nbox 9 x1\nshape 9 x1\nshape 9 x2\n[shape 9 x1]\nshape 9 x1\n"
	t.Run("the impl in the calling file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.{Shape, all}\n\n"+square+"\n"+calls, want,
			map[string]string{"shape.nomi": shape})
	})
	t.Run("the impl in a third file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.{Shape, all}\nimport square.Square\n\n"+calls, want,
			map[string]string{"shape.nomi": shape, "square.nomi": "import shape.Shape\n\n" + square})
	})
}
