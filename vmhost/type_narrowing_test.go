package vmhost_test

import "testing"

// Spec §8 *Type Narrowing*: in an arm that matched a name against an
// `embeds` variant, the name holds the embedded type. Before, the checker
// accepted `take(value)` (its unifier admits an enum where an embedded type
// is expected either way round) and the IR builder declined the call, so the
// program was accepted and could not run. Each shape here narrows: a `case`
// arm over a braced embedded struct, a guard, a lambda created in the arm, an
// `as` around the pattern, a name a destructure bound, an embedded distinct,
// and the first branch of `if Pattern = name`.
func TestTypeNarrowing_RunsInEveryArmShape(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
    radius: Float
}

type Tag String

enum Shape {
    embeds Circle
    embeds Tag
    Point
}

struct Wrap {
    shape: Shape
}

fn take(c: Circle): String {
    "circle ${c.radius}"
}

fn tag_text(t: Tag): String {
    case t {
        Tag(s) -> "tag ${s}"
    }
}

fn name(s: Shape): String {
    case s {
        .Circle{radius: _} -> "a circle"
        .Tag(_) -> "a tag"
        .Point -> "a point"
    }
}

fn plain(value: Shape): String {
    case value {
        .Circle{radius} -> "${take(value)} r=${radius}"
        .Tag(_) -> tag_text(value)
        .Point -> name(value)
    }
}

fn guarded(value: Shape): String {
    case value {
        .Circle{radius: _} when value.radius > 1.0 -> "big ${take(value)}"
        .Circle{radius: _} -> "small ${value.radius}"
        _ -> name(value)
    }
}

fn captured(value: Shape): String {
    case value {
        .Circle{radius: _} -> {
            f = || take(value)
            f()
        }
        _ -> "other"
    }
}

fn as_bound(value: Shape): String {
    case value {
        Shape.Circle{radius: _} as s -> "${take(value)}, ${name(s)}"
        _ -> "other"
    }
}

fn destructured(w: Wrap): String {
    Wrap{shape: s} = w
    case s {
        .Tag(_) -> tag_text(s)
        _ -> "not a tag"
    }
}

fn by_if(value: Shape): String {
    if .Circle{radius: _} = value {
        take(value)
    } else {
        "not a circle"
    }
}

fn by_if_distinct(value: Shape): String {
    if .Tag(_) = value {
        tag_text(value)
    } else {
        "not a tag"
    }
}

fn main() {
    io.print(plain(Circle{radius: 2.0}))
    io.print(plain(Shape.Tag("x")))
    io.print(plain(Shape.Point))
    io.print(guarded(Circle{radius: 2.0}))
    io.print(guarded(Circle{radius: 0.5}))
    io.print(guarded(Shape.Point))
    io.print(captured(Circle{radius: 3.0}))
    io.print(as_bound(Circle{radius: 4.0}))
    io.print(destructured(Wrap{shape: Shape.Tag("y")}))
    io.print(destructured(Wrap{shape: Shape.Point}))
    io.print(by_if(Circle{radius: 5.0}))
    io.print(by_if(Shape.Point))
    io.print(by_if_distinct(Shape.Tag("z")))
    io.print(by_if_distinct(Circle{radius: 1.0}))
}
`)
	want := `circle 2.0 r=2.0
tag x
a point
big circle 2.0
small 0.5
a point
circle 3.0
circle 4.0, a circle
tag y
not a tag
circle 5.0
not a circle
tag z
not a tag
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A narrowed value is still a value of its enum: it widens back wherever the
// enum is expected (a parameter, a result, a list element, a struct field, a
// type-qualified impl call, either side of `==`), and an interface call
// dispatches to the embedded type's own impl, as the spec says.
func TestTypeNarrowing_NarrowedValueWidensBackIntoTheEnum(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
    radius: Float
}

enum Shape {
    embeds Circle
    Point
}

struct Wrap {
    shape: Shape
}

derive Equatable for Circle

derive Equatable for Shape

impl Display for Circle {
    fn to_string(c: Circle): String {
        "circle of ${c.radius}"
    }
}

impl Display for Shape {
    fn to_string(s: Shape): String {
        case s {
            .Circle{radius: _} -> "a shape"
            .Point -> "a point"
        }
    }
}

fn same(value: Shape): Shape {
    case value {
        .Circle{radius: _} -> value
        .Point -> value
    }
}

fn describe(value: Shape, other: Shape): String {
    case value {
        .Circle{radius: _} -> {
            all: List<Shape> = [value, Shape.Point]
            w = Wrap{shape: value}
            "${Display.to_string(value)}; ${Shape.to_string(value)}; ${value == other} ${other != value}; ${Iter.count(all)}; ${Debug.inspect(w)}"
        }
        .Point -> Display.to_string(value)
    }
}

fn main() {
    io.print(describe(Circle{radius: 1.0}, Circle{radius: 1.0}))
    io.print(describe(Circle{radius: 1.0}, Shape.Point))
    io.print(describe(Shape.Point, Shape.Point))
    io.print(Display.to_string(same(Circle{radius: 2.0})))
}
`)
	want := `circle of 1.0; a shape; True False; 2; Wrap{shape: Circle{radius: 1.0}}
circle of 1.0; a shape; False True; 2; Wrap{shape: Circle{radius: 1.0}}
a point
a shape
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A struct field of an enum type takes a value of an embedded type from any
// expression, a call included, and the fields still run in source order.
func TestEmbedWiden_StructFieldFromACall(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
    radius: Float
}

enum Shape {
    embeds Circle
    Point
}

struct Wrap {
    shape: Shape
    n: Int
}

fn mk(): Circle {
    io.print("mk")
    Circle{radius: 2.0}
}

fn num(): Int {
    io.print("num")
    1
}

fn main() {
    io.inspect(Wrap{shape: mk(), n: num()})
    io.inspect(Wrap{n: num(), shape: mk()})
}
`)
	want := `mk
num
Wrap{shape: Circle{radius: 2.0}, n: 1}
num
mk
Wrap{shape: Circle{radius: 2.0}, n: 1}
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
