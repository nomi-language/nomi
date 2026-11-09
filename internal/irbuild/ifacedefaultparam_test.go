package irbuild

import (
	"strings"
	"testing"
)

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

// A generic interface's parameter default that mentions the interface's
// type parameter and calls a private helper of its file is filled by the
// declaring file's accessor, at the kinds the call's impl gives T: main.nomi
// declares a `pick` of its own, which the default must not reach.
// Defaults cannot name earlier parameters, so `pick` takes none and its T
// comes from the slot's type alone; `show`'s own T must not stand for it.
// The default used to be lowered with T unbound, in any file: "this call to
// `Square.describe` is not supported yet".
func TestIfaceDefault_GenericParamDefaultFilledInTheDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const shape = `pub interface Shape<T> {
    fn first(s: self) -> T

    fn describe(s: self, x: (List<T>, String) = pick()) -> String {
        (xs, who) = x
        "${who} ${Iter.count([first(s), ..xs])}"
    }
}

fn pick<T>() -> (List<T>, String) {
    ([], "shape")
}
`
	const impls = `pub struct Square {
    side: Int
}

pub struct Pair<E> {
    a: E
    b: E
}

impl Shape for Square {
    fn first(s: Square) -> Int {
        s.side
    }
}

impl Shape for Pair<E> {
    fn first(p: Pair<E>) -> E {
        p.a
    }
}
`
	const mainPick = `fn pick<T>() -> (List<T>, String) {
    ([], "main")
}

`
	// A generic caller whose own type parameter is also named T.
	const show = `fn show<T>(p: Pair<T>) -> String {
    Pair.describe(p)
}

`
	const showCall = "    io.print(show(Pair{a: True, b: False}))\n"
	const calls = `fn main() {
    sq = Square{side: 3}
    io.print(Square.describe(sq))
    io.print(Square.describe(sq, ([1, 2], "given")))
    io.print(Pair.describe(Pair{a: "x", b: "y"}))
    io.print(Pair.describe(Pair{a: 1.5, b: 2.5}))
//SHOW
    mine: (List<Int>, String) = pick()
    (none, who) = mine
    io.print("${who} ${Iter.count(none)}")
}
`
	withShow := show + strings.Replace(calls, "//SHOW\n", showCall, 1)
	const want = "shape 1\ngiven 3\nshape 1\nshape 1\n"
	t.Run("the impls in the calling file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\n\n"+impls+"\n"+mainPick+withShow, want+"shape 1\nmain 0\n",
			map[string]string{"shape.nomi": shape})
	})
	t.Run("the impls in a third file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shapes.{Square, Pair}\n\n"+mainPick+withShow, want+"shape 1\nmain 0\n",
			map[string]string{"shape.nomi": shape, "shapes.nomi": "import shape.Shape\n\n" + impls})
	})
	t.Run("the impls in the declaring file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+shape+"\n"+impls+"\n"+withShow, want+"shape 1\nshape 0\n")
	})
}
