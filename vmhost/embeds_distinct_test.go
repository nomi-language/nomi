package vmhost_test

import (
	"strings"
	"testing"
)

// An enum that embeds a distinct type over a struct, an enum or a generic
// struct instance (spec §8, Embedded Types). `Shape.Wrapped(c)` builds a
// Wrapped from the inner Circle, as `Shape.UserId(8)` builds a UserId from
// an Int, and the pattern `Shape.Wrapped(c)` binds the inner Circle. Every
// form declined in the IR builder: the enum was outside the retained domain,
// so construction was "qualified call, Type.method: Shape.Wrapped" and a
// widening binding "a binding annotation outside the retained value domain".
const embedsCompositeDecls = `struct Circle {
    r: Int
}

type Wrapped Circle

type Tagged Color

type Boxed Box<Int>

enum Color {
    Red
    Green
}

struct Box<T> {
    value: T
}
`

const embedsCompositeEnum = `enum Shape {
    embeds Wrapped
    embeds Tagged
    embeds Boxed
    Dot
}
`

const embedsCompositeUse = `fn describe(s: Shape): String {
    case s {
        Shape.Wrapped(c) -> "circle ${c.r}"
        Shape.Tagged(c) -> case c {
            .Red -> "red"
            .Green -> "green"
        }
        Shape.Boxed(b) -> "box ${b.value}"
        .Dot -> "dot"
    }
}

fn radius(s: Shape): Int {
    Shape.Wrapped(c) = s else {
        return 0
    }
    c.r
}

fn circle(r: Int): Circle {
    io.print("circle ${r}")
    Circle{r: r}
}

fn main() {
    shapes = [
        Shape.Wrapped(circle(1)),
        Shape.Tagged(Color.Green),
        Shape.Boxed(Box{value: 4}),
        Circle{r: 2} |> Shape.Wrapped(),
        Shape.Dot,
    ]
    io.print(Debug.inspect(shapes))
    shapes |> Iter.map(describe) |> String.join(", ") |> io.print()
    io.print("${radius(Shape.Wrapped(Circle{r: 9}))} ${radius(Shape.Dot)}")
    widened: List<Shape> = [Wrapped(circle(3)), Tagged(Color.Red), Boxed(Box{value: 5})]
    io.print(Debug.inspect(widened))
    t: Shape = Tagged(Color.Red)
    io.print("${Shape.Tagged(Color.Red) == t} ${Shape.Boxed(Box{value: 5}) == Shape.Boxed(Box{value: 6})}")
    w: Shape = Wrapped(Circle{r: 7})
    io.print("${Shape.Wrapped(Circle{r: 7}) == w}")
    [Circle{r: 8}] |> Iter.map(Shape.Wrapped) |> Iter.map(Debug.inspect) |> String.join(", ") |> io.print()
}
`

func TestEmbedsDistinct_OverAStructAnEnumAndAGenericStruct(t *testing.T) {
	want := strings.Join([]string{
		"circle 1",
		"[Wrapped(Circle{r: 1}), Tagged(Green), Boxed(Box{value: 4}), Wrapped(Circle{r: 2}), Dot]",
		"circle 1, green, box 4, circle 2, dot",
		"9 0",
		"circle 3",
		"[Wrapped(Circle{r: 3}), Tagged(Red), Boxed(Box{value: 5})]",
		"True False",
		"True",
		"Wrapped(Circle{r: 8})",
		"",
	}, "\n")
	for name, src := range map[string]string{
		"enum first":     embedsCompositeEnum + "\n" + embedsCompositeDecls,
		"distinct first": embedsCompositeDecls + "\n" + embedsCompositeEnum,
	} {
		got := runSourceOutput(t, "import std/io\n\n"+src+"\n"+embedsCompositeUse)
		if got != want {
			t.Errorf("%s: output\n%s\nwant\n%s", name, got, want)
		}
	}
}

// In a generic enum, constructing an embedded or struct-shaped variant
// leaves the enum's parameter open, as an ordinary variant's constructor
// does, so `Shape.UserId(3)` fits a `Shape<Int>` parameter. The checker
// typed it `Shape<T>` and rejected it ("expected Shape<Int>, got Shape<T>"),
// and the IR builder found no instance for it: such a construction records
// no call signature, and `Shape.Expired` of a generic enum was not lowered.
func TestEmbedsDistinct_GenericEnumConstructionTakesItsInstance(t *testing.T) {
	got := runSourceOutput(t, `import std/io

type UserId Int

type Expired

struct Circle {
    r: Int
}

enum Shape<T> {
    embeds UserId
    embeds Circle
    embeds Expired
    Val(T)
    Rect {w: Int}
}

fn first(s: Shape<Int>): Int {
    case s {
        Shape.UserId(n) -> n
        Shape.Circle{r} -> r
        Shape.Expired -> 7
        Shape.Val(v) -> v
        Shape.Rect{w} -> w
    }
}

fn main() {
    io.print("${first(Shape.UserId(3))} ${first(4 |> Shape.UserId())} ${first(Shape.Val(5))}")
    io.print("${first(Shape.Circle({r: 6}))} ${first({r: 8} |> Shape.Circle())} ${first(Shape.Expired)}")
    io.print("${first(Shape.Rect({w: 12}))} ${first({w: 13} |> Shape.Rect())}")
    t: Shape<String> = Shape.UserId(9)
    io.print(Debug.inspect(t))
    xs = [Shape.UserId(10), Shape.Val("a")]
    io.print(Debug.inspect(xs))
    f: (Int) -> Shape<Float> = Shape.UserId
    io.print(Debug.inspect([f(11), Shape.Val(1.5)]))
}
`)
	want := "3 4 5\n6 8 7\n12 13\nUserId(9)\n[UserId(10), Val(\"a\")]\n[UserId(11), Val(1.5)]\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
