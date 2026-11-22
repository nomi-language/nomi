package vmhost_test

import "testing"

// `T.inspect(x)` renders x as `Debug.inspect(x)` does for every type: a
// module-level type and one declared in a body, generic or not, with a
// written Debug or none, and named as a value.
func TestUniversalDebug_TypeQualifiedInspectRendersEveryType(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct Point
    x: Int
    y: Int
end

struct Box<T>
    v: T
end

struct Shown
    n: Int
end

impl Debug for Shown
    fn inspect(value: Shown) -> String
        "S${value.n}"
    end
end

fn main()
    struct Pair<T>
        a: T
        b: Shown
    end

    enum Tree
        Leaf
        Node(Tree, Int, Tree)
    end

    opaque struct Hidden
        n: Int
    end

    type Meters Int

    io.print(Point.inspect(Point{x: 1, y: 2}))
    io.print(Box.inspect(Box{v: Point{x: 3, y: 4}}))
    io.print(Shown.inspect(Shown{n: 5}))
    io.print(Pair.inspect(Pair{a: "x", b: Shown{n: 6}}))
    io.print(Tree.inspect(Tree.Node(Tree.Leaf, 7, Tree.Leaf)))
    io.print(Hidden.inspect(Hidden{n: 8}))
    io.print(Meters.inspect(Meters(9)))
    f = Point.inspect
    io.print(f(Point{x: 10, y: 11}))
    g = Tree.inspect
    io.print(g(Tree.Leaf))
    texts = [Meters(12), Meters(13)] |> Iter.map(Meters.inspect) |> Iter.to_list()
    io.print(String.join(texts, ", "))
    io.print(String.join([Box{v: 14}] |> Iter.map(Box.inspect) |> Iter.to_list(), ", "))
    io.print(Hidden{n: 1}.n == 1)
end
`)
	want := `Point{x: 1, y: 2}
Box{v: Point{x: 3, y: 4}}
S5
Pair{a: "x", b: S6}
Node(Leaf, 7, Leaf)
<opaque Hidden>
Meters(9)
Point{x: 10, y: 11}
Leaf
Meters(12), Meters(13)
Box{v: 14}
True
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// Every type implements Debug, so a value of any type is a `Debug` value
// and meets a `where T: Debug` bound: a struct with no Debug impl, one with
// a written one, a tuple and a type declared in a body, held in a
// `List<Debug>`, alone as a `Debug`, and passed to a bounded function,
// render as `Debug.inspect` renders each.
func TestUniversalDebug_InterfaceValuesAndBoundsRenderEveryType(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct Point
    x: Int
end

struct Shown
    n: Int
end

impl Debug for Shown
    fn inspect(value: Shown) -> String
        "S${value.n}"
    end
end

fn show<T>(x: T) -> String where T: Debug
    Debug.inspect(x)
end

fn main()
    struct Local
        a: Int
    end

    xs: List<Debug> = [Point{x: 1}, Shown{n: 2}, (3, "a"), Local{a: 4}, Some(Point{x: 5})]
    io.print(Debug.inspect(xs))
    io.print(String.join(xs |> Iter.map(Debug.inspect) |> Iter.to_list(), "; "))
    d: Debug = Point{x: 6}
    io.print(Debug.inspect(d))
    io.print(show(d))
    io.print(show(Point{x: 7}))
    io.print(show(Local{a: 8}))
    io.print(show(Shown{n: 9}))
    io.print(show((10, Point{x: 11})))
end
`)
	want := `[Point{x: 1}, S2, (3, "a"), Local{a: 4}, Some(Point{x: 5})]
Point{x: 1}; S2; (3, "a"); Local{a: 4}; Some(Point{x: 5})
Point{x: 6}
Point{x: 6}
Point{x: 7}
Local{a: 8}
S9
(10, Point{x: 11})
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A positional variant whose payload is a struct of the variant's name
// renders as the call that builds it, with no Debug impl and with a derived
// one, at the top level, in a list and held as a `Debug` value.
func TestUniversalDebug_AVariantOfASameNamedStructIsItsCall(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct Click
    x: Int
end

enum Event
    Click Click
    Key String
end

enum Logged
    Click Click
end

derive Debug for Logged

fn main()
    io.inspect(Event.Click(Click{x: 1}))
    io.inspect([Event.Click(Click{x: 2}), Event.Key("k")])
    io.inspect(Logged.Click(Click{x: 3}))
    xs: List<Debug> = [Event.Click(Click{x: 4}), Logged.Click(Click{x: 5})]
    io.inspect(xs)
end
`)
	want := `Click(Click{x: 1})
[Click(Click{x: 2}), Key("k")]
Click(Click{x: 3})
[Click(Click{x: 4}), Click(Click{x: 5})]
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
