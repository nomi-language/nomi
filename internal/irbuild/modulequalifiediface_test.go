package irbuild

import "testing"

// `impl calendar.DateParts for Q` beside a local `interface DateParts`,
// reached through a `where T: calendar.DateParts` bound, a
// `calendar.DateParts` parameter, and the local interface's own bound.
// The front end filed the qualified impl as one of the local interface and
// refused the bound and the parameter.
func TestIRModuleQualifiedIface_StdBesideALocalInterfaceOfItsName(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::calendar std::io
end

interface DateParts
    fn stamp(value: self) -> String
end

struct Q
    n: Int
end

impl calendar.DateParts for Q
    fn year(value: Q) -> Int
        value.n
    end

    fn month(_: Q) -> Int
        1
    end

    fn day(_: Q) -> Int
        2
    end
end

impl DateParts for Q
    fn stamp(_: Q) -> String
        "local"
    end
end

fn total<T>(x: T, y: T) -> Int where T: calendar.DateParts
    calendar.DateParts.year(x) + calendar.DateParts.month(y)
end

fn as_value(x: calendar.DateParts) -> Int
    calendar.DateParts.day(x)
end

fn local<T>(x: T, y: T) -> String where T: DateParts
    DateParts.stamp(x) + DateParts.stamp(y)
end

fn main()
    q = Q{n: 7}
    io.print(total(q, q))
    io.print(as_value(q))
    io.print(local(q, q))
end
`, "8\n2\nlocallocal\n")
}

const moduleQualifiedOther = `pub interface Shape
    fn area(value: self) -> Int
end
`

// `impl other.Shape for Sq` for another file's interface. Beside a local
// `interface Shape` the impl was bound into the local interface's table and
// a call `other.Shape.area(x)` was refused; with no local `Shape` the impl
// was bound into no table, so a value held as `other.Shape` found no
// implementation for Sq.
func TestIRModuleQualifiedIface_ProgramModule(t *testing.T) {
	t.Run("beside a local interface of its name", func(t *testing.T) {
		verifyLambdaProgram(t, `import
    other std::io
end

interface Shape
    fn label(value: self) -> String
end

struct Sq
    side: Int
end

impl other.Shape for Sq
    fn area(value: Sq) -> Int
        value.side * value.side
    end
end

impl Shape for Sq
    fn label(_: Sq) -> String
        "square"
    end
end

fn double<T>(x: T, y: T) -> Int where T: other.Shape
    other.Shape.area(x) + other.Shape.area(y)
end

fn area_of(x: other.Shape) -> Int
    other.Shape.area(x)
end

fn labels<T>(x: T, y: T) -> String where T: Shape
    Shape.label(x) + Shape.label(y)
end

fn main()
    s = Sq{side: 3}
    io.print(double(s, s))
    io.print(area_of(s))
    io.print(labels(s, s))
    io.print(other.Shape.area(s))
    io.print(Shape.label(s))
    shapes: List<other.Shape> = [s, Sq{side: 2}]
    io.print(shapes |> Iter.map(fn x -> other.Shape.area(x)) |> Iter.sum())
end
`, "18\n9\nsquaresquare\n9\nsquare\n13\n", map[string]string{"other.nomi": moduleQualifiedOther})
	})
	t.Run("alone", func(t *testing.T) {
		verifyLambdaProgram(t, `import
    other std::io
end

struct Sq
    side: Int
end

impl other.Shape for Sq
    fn area(value: Sq) -> Int
        value.side * value.side
    end
end

fn double<T>(x: T, y: T) -> Int where T: other.Shape
    other.Shape.area(x) + other.Shape.area(y)
end

fn area_of(x: other.Shape) -> Int
    other.Shape.area(x)
end

fn main()
    s = Sq{side: 3}
    io.print(double(s, s))
    io.print(area_of(s))
end
`, "18\n9\n", map[string]string{"other.nomi": moduleQualifiedOther})
	})
}
