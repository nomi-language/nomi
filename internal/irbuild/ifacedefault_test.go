package irbuild

import (
	"strings"
	"testing"
)

// ifaceDefaultShape declares an interface whose defaults name what only its
// own file binds: private functions, a `once`, an import, a generic helper.
// Its defaults call the interface's functions and each other, one is open,
// one is generic, and the file holds a bounded generic over the interface.
const ifaceDefaultShape = `import std::hex

pub interface Shape
    fn area(s: self) -> Int

    fn describe(s: self) -> String
        label(area(s))
    end

    fn coded(s: self) -> String
        hex.encode(String.to_bytes(describe(s))) + suffix
    end

    open fn doubled(s: self) -> Int
        2 * area(s)
    end

    fn report(s: self) -> String
        "${Shape.describe(s)} doubled ${doubled(s)}"
    end

    fn pick<T>(s: self, a: T, b: T) -> T
        if big(area(s)) then a else b
    end

    fn same(s: self, t: self) -> Bool
        area(s) == area(t)
    end

    fn shown(s: self) -> String
        Debug.inspect(s)
    end

    fn twin(s: self) -> (self, self)
        (s, s)
    end
end

once suffix = "!"

fn label(n: Int) -> String
    "area ${n}"
end

fn big(n: Int) -> Bool
    n > 10
end

pub fn via_bound<T>(x: T) -> String where T: Shape
    Shape.report(x)
end
`

// ifaceDefaultImpls implements Shape for a struct, a distinct type that
// overrides the open default, a named tuple and Int, none writing a default.
const ifaceDefaultImpls = `struct Square
    side: Int
end

type Meters Int

type Span (Int, Int)

impl Shape for Square
    fn area(s: Square) -> Int
        s.side * s.side
    end
end

impl Shape for Meters
    fn area(m: Meters) -> Int
        Meters(n) = m
        n * n
    end

    fn doubled(_m: Meters) -> Int
        0
    end
end

impl Shape for Span
    fn area(s: Span) -> Int
        s.first * s.second
    end
end

impl Shape for Int
    fn area(n: Int) -> Int
        n
    end
end
`

// ifaceDefaultTri implements Shape for a type whose Debug is derived, in a
// file of its own.
const ifaceDefaultTri = `pub struct Tri
    base: Int
end

derive Debug for Tri

impl Shape for Tri
    fn area(t: Tri) -> Int
        t.base * 2
    end
end
`

const ifaceDefaultCalls = `
fn local_bound<T>(x: T) -> String where T: Shape
    Shape.coded(x)
end

fn main()
    sq = Square{side: 3}
    io.print(Square.describe(sq))
    io.print(Shape.describe(sq))
    io.print(Square.coded(sq))
    io.print(Square.report(sq))
    io.print(Square.pick(Square{side: 4}, "big", "small"))
    io.inspect(Shape.pick(sq, 1, 2))
    io.inspect(Square.same(sq, Square{side: 3}))
    io.print(Square.shown(sq))
    io.inspect(Square.twin(sq))
    io.print(Meters.report(Meters(2)))
    io.print(Span.describe(Span(2, 5)))
    io.print(Shape.coded(Span(2, 5)))
    io.print(Int.report(6))
    io.print(Tri.report(Tri{base: 4}))
    io.print(Tri.shown(Tri{base: 4}))
    io.inspect(Shape.pick(Tri{base: 9}, 1, 2))
    io.print(via_bound(sq))
    io.print(local_bound(Meters(5)))
    io.print(via_bound(Tri{base: 1}))
    shapes: List<Shape> = [sq, Meters(3), Span(1, 1), Tri{base: 2}, 7]
    io.inspect(shapes |> Iter.map(Shape.report) |> Iter.to_list())
end
`

const ifaceDefaultWant = "area 9\narea 9\n617265612039!\narea 9 doubled 18\nbig\n2\nTrue\n" +
	"Square{side: 3}\n(Square{side: 3}, Square{side: 3})\narea 4 doubled 0\narea 10\n" +
	"61726561203130!\narea 6 doubled 12\narea 8 doubled 16\nTri{base: 4}\n1\n" +
	"area 9 doubled 18\n61726561203235!\narea 2 doubled 4\n" +
	`["area 9 doubled 18", "area 9 doubled 0", "area 1 doubled 2", "area 4 doubled 8", "area 7 doubled 14"]` + "\n"

// An interface default is lowered in the file that declares it, once per
// implementing type, so its names are that file's wherever the implementing
// block is: `label` and `big` are private to shape.nomi, `suffix` is its
// `once` and `hex` its import. A block in another file calls the declaring
// file's instance, and the instance's own calls of the interface's
// functions reach that block's, so Meters' override of `doubled` answers
// for `report`. The default used to be copied into the implementing file,
// where `label` names nothing: "this call to `Square.describe` is not
// supported yet".
func TestIfaceDefault_LoweredInItsDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("the impls in the calling file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.{Shape, via_bound}\nimport tris.Tri\n\n"+ifaceDefaultImpls+ifaceDefaultCalls,
			ifaceDefaultWant, map[string]string{
				"shape.nomi": ifaceDefaultShape,
				"tris.nomi":  "import shape.Shape\n\n" + ifaceDefaultTri,
			})
	})
	t.Run("the impls in a third file", func(t *testing.T) {
		impls := "import shape.Shape\n\n" + publicTypes(ifaceDefaultImpls)
		verifyLambdaProgram(t, "import std::io\nimport shape.{Shape, via_bound}\nimport shapes.{Square, Meters, Span}\nimport tris.Tri\n"+ifaceDefaultCalls,
			ifaceDefaultWant, map[string]string{
				"shape.nomi":  ifaceDefaultShape,
				"shapes.nomi": impls,
				"tris.nomi":   "import shape.Shape\n\n" + ifaceDefaultTri,
			})
	})
	t.Run("the impls in the declaring file", func(t *testing.T) {
		shape := strings.TrimPrefix(ifaceDefaultShape, "import std::hex\n")
		verifyLambdaProgram(t, "import\n    std::hex\n    std::io\nend\n"+shape+"\n"+
			ifaceDefaultImpls+ifaceDefaultTri+ifaceDefaultCalls, ifaceDefaultWant)
	})
}

// publicTypes makes ifaceDefaultImpls' types public, for a file other files
// import them from.
func publicTypes(src string) string {
	for _, decl := range []string{"struct Square", "type Meters", "type Span"} {
		src = strings.Replace(src, decl, "pub "+decl, 1)
	}
	return src
}
