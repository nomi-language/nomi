package vmhost_test

import "testing"

// A derived Display writes no function: the machine renders the value by
// its shape (rt.DisplayShapeText), each part through the Display body the
// record of the part's type names. The output is what the synthesized
// bodies printed: a struct's fields in declaration order, each variant
// shape, a distinct over a scalar, a tuple, a map, a vector and a list,
// a marker, a recursive enum, generic types, nested Strings unquoted, a
// Decimal at its scale, a Float with its `.0`, a Byte and a Bytes, a
// Range, an interface value, std containers holding derived types, and
// `Display.to_string`, a bound and a hole alike.
func TestDerivedDisplay_IsTheShape(t *testing.T) {
	got := runSourceOutput(t, `import
    std::io
end

struct Point
    y: Int
    x: Int
end

derive Display for Point

struct Empty
end

derive Display for Empty

struct Money
    cents: Int
end

impl Display for Money
    fn to_string(m: Money) -> String
        "$${m.cents}"
    end
end

type Id Int

derive Display for Id

type Name String

derive Display for Name

type Pair (Int, String)

derive Display for Pair

type Kvs Map<String, Int>

derive Display for Kvs

type Path Vector<Int>

derive Display for Path



type Items List<Point>

derive Display for Items

type Expired

derive Display for Expired

struct Circle
    radius: Float
end

derive Display for Circle

struct Click
    x: Int
end

derive Display for Click

enum Shape
    Dot
    Round(Float)
    Two(Int, String)
    Wrapped((Int, Int))
    Rect{w: Int, label: String}
    Click(Click)
    embeds Circle
    embeds Id
    embeds Expired
    embeds Money
end

derive Display for Shape

enum Tree
    Leaf(Int)
    Node(Tree, Tree)
end

derive Display for Tree

struct Box<T>
    value: T
    note: String
end

derive Display for Box

enum Either<A, B>
    Left(A)
    Right(B)
end

derive Display for Either

struct All
    s: String
    i: Int
    f: Float
    d: Decimal
    b: Bool
    by: Byte
    bs: Bytes
    o: Option<String>
    r: Result<Int, String>
    l: List<String>
    m: Map<String, Point>
    v: Vector<String>
    st: Set<String>
    t: (Int, String, Point)
    an: {z: String, a: Int}
    rg: Range<Int>
    money: Money
    shown: Display
    boxed: Box<Money>
    either: Either<String, Money>
    big: Int
end

derive Display for All

fn show<T>(x: T) -> String where T: Display
    "<${x}>"
end

fn main()
    a: Byte = 200
    c: Byte = 97
    io.print(Point{y: 1, x: 2})
    io.print(Empty{})
    io.print(Id(3))
    io.print(Name("bob"))
    io.print(Pair((1, "x")))
    io.print(Kvs({"a" => 1, "b" => 2}))
    io.print(Path(Vector[1, 2]))
    io.print(Items([Point{y: 1, x: 2}]))
    io.print(Expired)
    shapes = [
        Shape.Dot,
        Shape.Round(1.5),
        Shape.Two(1, "two"),
        Shape.Wrapped((3, 4)),
        Shape.Rect{w: 2, label: "r"},
        Shape.Click(Click{x: 9}),
        Circle{radius: 2.0},
        Id(7),
        Expired,
        Money{cents: 12},
    ]
    io.print(shapes)
    io.print(shapes |> Iter.map(show) |> String.join("; "))
    io.print(shapes |> Iter.map(Display.to_string) |> String.join("; "))
    io.print(Tree.Node(Tree.Leaf(1), Tree.Node(Tree.Leaf(2), Tree.Leaf(3))))
    io.print(Box{value: "s", note: "n"})
    io.print(Box{value: Money{cents: 3}, note: "n"})
    io.print(Box{value: Box{value: Point{y: 0, x: 0}, note: "in"}, note: "out"})
    l: Either<String, Money> = Either.Left("l")
    r: Either<String, Money> = Either.Right(Money{cents: 4})
    io.print(l)
    io.print(r)
    all = All{
        s: "str",
        i: -1,
        f: 2.0,
        d: 1.50d,
        b: True,
        by: a,
        bs: Bytes.from_list([a, c]),
        o: Some("o"),
        r: Err("e"),
        l: ["x", "y"],
        m: {"p" => Point{y: 1, x: 1}},
        v: Vector["v"],
        st: Set["s"],
        t: (1, "t", Point{y: 2, x: 3}),
        an: {z: "zz", a: 1},
        rg: 1..3,
        money: Money{cents: 5},
        shown: Money{cents: 6},
        boxed: Box{value: Money{cents: 7}, note: "bx"},
        either: Either.Right(Money{cents: 8}),
        big: 123456789012345678901234567890,
    }
    io.print(all)
    io.print("${all}")
    io.print(show(all))
    io.print(Some(Point{y: 1, x: 2}))
    io.print([Some(Shape.Dot), None])
    io.print((Point{y: 1, x: 2}, "q"))
    io.print({k: Point{y: 1, x: 2}})
    io.print([Point{y: 1, x: 2}])
    io.print({"k" => Shape.Two(1, "x")})
end
`)
	const want = `Point{y: 1, x: 2}
Empty{}
Id(3)
Name(bob)
Pair(1, x)
Kvs({a => 1, b => 2})
Path(Vector[1, 2])
Items([Point{y: 1, x: 2}])
Expired
[Dot, Round(1.5), Two(1, two), Wrapped(3, 4), Rect{w: 2, label: r}, Click(Click{x: 9}), Circle{radius: 2.0}, Id(7), Expired, $12]
<Dot>; <Round(1.5)>; <Two(1, two)>; <Wrapped(3, 4)>; <Rect{w: 2, label: r}>; <Click(Click{x: 9})>; <Circle{radius: 2.0}>; <Id(7)>; <Expired>; <$12>
Dot; Round(1.5); Two(1, two); Wrapped(3, 4); Rect{w: 2, label: r}; Click(Click{x: 9}); Circle{radius: 2.0}; Id(7); Expired; $12
Node(Leaf(1), Node(Leaf(2), Leaf(3)))
Box{value: s, note: n}
Box{value: $3, note: n}
Box{value: Box{value: Point{y: 0, x: 0}, note: in}, note: out}
Left(l)
Right($4)
All{s: str, i: -1, f: 2.0, d: 1.50, b: True, by: 200, bs: Bytes(2), o: Some(o), r: Err(e), l: [x, y], m: {p => Point{y: 1, x: 1}}, v: Vector[v], st: Set[s], t: (1, t, Point{y: 2, x: 3}), an: {a: 1, z: zz}, rg: 1..3, money: $5, shown: $6, boxed: Box{value: $7, note: bx}, either: Right($8), big: 123456789012345678901234567890}
All{s: str, i: -1, f: 2.0, d: 1.50, b: True, by: 200, bs: Bytes(2), o: Some(o), r: Err(e), l: [x, y], m: {p => Point{y: 1, x: 1}}, v: Vector[v], st: Set[s], t: (1, t, Point{y: 2, x: 3}), an: {a: 1, z: zz}, rg: 1..3, money: $5, shown: $6, boxed: Box{value: $7, note: bx}, either: Right($8), big: 123456789012345678901234567890}
<All{s: str, i: -1, f: 2.0, d: 1.50, b: True, by: 200, bs: Bytes(2), o: Some(o), r: Err(e), l: [x, y], m: {p => Point{y: 1, x: 1}}, v: Vector[v], st: Set[s], t: (1, t, Point{y: 2, x: 3}), an: {a: 1, z: zz}, rg: 1..3, money: $5, shown: $6, boxed: Box{value: $7, note: bx}, either: Right($8), big: 123456789012345678901234567890}>
Some(Point{y: 1, x: 2})
[Some(Dot), None]
(Point{y: 1, x: 2}, q)
{k: Point{y: 1, x: 2}}
[Point{y: 1, x: 2}]
{k => Two(1, x)}
`
	if got != want {
		t.Fatalf("derived Display:\n got: %q\nwant: %q", got, want)
	}
}
