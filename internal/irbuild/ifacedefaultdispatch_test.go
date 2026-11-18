package irbuild

import (
	"strings"
	"testing"
)

// ifaceDispatchShape declares an `open` interface function whose two
// parameters have defaults; `tail`'s reads the receiver and `scale`. Each
// default goes through `note`, private to this file, which prints its
// label, so a run shows which defaults ran, once each, after the written
// arguments.
const ifaceDispatchShape = `fn note(label: String, n: Int) -> Int
    io.print(label)
    n
end

pub interface Shape
    fn side(value: self) -> Int

    open fn area(value: self, scale: Int = note("shape scale", 1), tail: Int = note("shape tail", scale + Shape.side(value))) -> Int
        Shape.side(value) * scale + tail
    end
end
`

// ifaceDispatchImpls implements Shape four ways: Circle and the generic
// Box declare their own defaults, which read the receiver and `scale`;
// Dot inherits the interface's function; Tri writes the function with no
// defaults; a List<Int> declares a default for `scale` only.
const ifaceDispatchImpls = `fn mark(label: String, n: Int) -> Int
    io.print(label)
    n
end

pub struct Circle
    r: Int
end

impl Shape for Circle
    fn side(c: Circle) -> Int
        c.r
    end

    fn area(c: Circle, scale: Int = mark("circle scale", c.r * 10), tail: Int = mark("circle tail", scale * 2)) -> Int
        c.r * scale + tail
    end
end

pub struct Dot
    n: Int
end

impl Shape for Dot
    fn side(d: Dot) -> Int
        d.n
    end
end

pub struct Tri
    n: Int
end

impl Shape for Tri
    fn side(t: Tri) -> Int
        t.n
    end

    fn area(t: Tri, scale: Int, tail: Int) -> Int
        t.n * scale + tail + 1000
    end
end

pub struct Box<T>
    v: T
    n: Int
end

impl<T> Shape for Box<T>
    fn side(b: Box<T>) -> Int
        b.n
    end

    fn area(b: Box<T>, scale: Int = mark("box scale", b.n + 1), tail: Int = mark("box tail", scale * 3)) -> Int
        b.n * scale + tail
    end
end

impl Shape for List<Int>
    fn side(xs: List<Int>) -> Int
        List.length(xs)
    end

    fn area(xs: List<Int>, scale: Int = mark("list scale", 7), tail: Int) -> Int
        List.length(xs) * scale + tail
    end
end
`

const ifaceDispatchHelpers = `fn written(n: Int) -> Int
    io.print("written")
    n
end

fn via<T>(x: T) -> Int where T: Shape
    T.area(x)
end

fn erased(s: Shape) -> Int
    Shape.area(s)
end
`

// ifaceDispatchConcrete are the calls whose receiver's type is known where
// they are lowered: they reach Circle's function and its defaults.
const ifaceDispatchConcrete = `    io.print("direct ${Circle.area(c)}")
    io.print("qualified ${Shape.area(c)}")
    io.print("type parameter ${via(c)}")
    f = &Shape.area(&1)
    io.print("partial ${f(c)}")
`

// ifaceDispatchErased are the calls on a Shape value, which choose the
// default when they run.
const ifaceDispatchErased = `    io.print("parameter ${erased(c)}")
    s: Shape = c
    io.print("binding ${Shape.area(s)}")
    io.print("piped ${s |> Shape.area()}")
    io.print("positional ${Shape.area(s, written(3))}")
    io.print("named ${Shape.area(s, tail: written(100))}")
    g = &Shape.area(&1)
    io.print("partial on a Shape ${g(s)}")
    shapes: List<Shape> = [c, Dot{n: 3}, Tri{n: 4}, Box{v: "x", n: 5}, [1, 2, 3]]
    io.print("each ${shapes |> Iter.map(&Shape.area(&1)) |> Iter.to_list()}")
`

const ifaceDispatchCircle = "circle scale\ncircle tail\n"

const ifaceDispatchConcreteWant = ifaceDispatchCircle + "direct 80\n" +
	ifaceDispatchCircle + "qualified 80\n" +
	ifaceDispatchCircle + "type parameter 80\n" +
	ifaceDispatchCircle + "partial 80\n"

const ifaceDispatchErasedWant = ifaceDispatchCircle + "parameter 80\n" +
	ifaceDispatchCircle + "binding 80\n" +
	ifaceDispatchCircle + "piped 80\n" +
	"written\ncircle tail\npositional 12\n" +
	"written\ncircle scale\nnamed 140\n" +
	ifaceDispatchCircle + "partial on a Shape 80\n" +
	ifaceDispatchCircle + "shape scale\nshape tail\nshape scale\nshape tail\nbox scale\nbox tail\nlist scale\nshape tail\n" +
	"each [80, 7, 1009, 48, 31]\n"

// ifaceDispatchMain is a main making the Circle c, then the calls.
func ifaceDispatchMain(calls string) string {
	return ifaceDispatchHelpers + "\nfn main()\n    c = Circle{r: 2}\n" + calls + "end\n"
}

// A call that leaves a defaulted parameter of an interface function out
// takes the default of the function that runs, per parameter: the impl
// function's when it declares one, else the interface's. On an interface
// value the impl is chosen when the call runs, and so is the default:
// a parameter, a binding, a pipe, a written or named argument before or
// after the slot, a partial and a list of values with five impls.
func TestIfaceDefault_DispatchedCallTakesTheImplsDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+ifaceDispatchShape+"\n"+ifaceDispatchImpls+"\n"+ifaceDispatchMain(ifaceDispatchConcrete+ifaceDispatchErased),
			ifaceDispatchConcreteWant+ifaceDispatchErasedWant)
	})
	// `note` is private to shape.nomi and `mark` to impls.nomi, so each
	// default is lowered in its own file: main.nomi calls the accessors
	// impls.nomi builds for Circle's function, directly on a Circle and
	// dispatched on a Shape.
	t.Run("the interface and its impls in other files", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport impls.Circle\nimport impls.Dot\nimport impls.Tri\nimport impls.Box\n\n"+ifaceDispatchMain(ifaceDispatchConcrete+ifaceDispatchErased), ifaceDispatchConcreteWant+ifaceDispatchErasedWant,
			map[string]string{
				"shape.nomi": "import std::io\n\n" + ifaceDispatchShape,
				"impls.nomi": "import std::io\nimport shape.Shape\n\n" + ifaceDispatchImpls,
			})
	})
}

// The same dispatch in a test's body.
func TestIfaceDefault_DispatchedCallTakesTheImplsDefaultInATest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := "import std::io\n\n" + ifaceDispatchShape + "\n" + ifaceDispatchImpls + `
test "a call on a Shape takes its impl's default"
    s: Shape = Circle{r: 2}
    assert Shape.area(s) == 80
    d: Shape = Dot{n: 3}
    assert Shape.area(d, 2) == 11
end
`
	out := irTestBodyVM(t, src, 1)
	want := "  circle scale\n  circle tail\n  shape tail\n"
	if !strings.Contains(out, want) || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the case to pass with the defaults' output\n%s\ngot:\n%s", want, out)
	}
}

// A generic interface's instance dispatches its defaults as a plain
// interface does: Cup declares its own, Mug inherits the interface's.
func TestIfaceDefault_DispatchedCallOnAGenericInterface(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::io

fn note(label: String, n: Int) -> Int
    io.print(label)
    n
end

interface Holder<T>
    open fn hold(_h: self, x: T, extra: Int = note("holder extra", 1)) -> Int
        List.length([x]) + extra
    end
end

struct Cup
    n: Int
end

impl Holder<String> for Cup
    fn hold(c: Cup, x: String, extra: Int = note("cup extra", c.n)) -> Int
        String.length(x) + extra
    end
end

struct Mug
    n: Int
end

impl Holder<String> for Mug end

fn main()
    hs: List<Holder<String>> = [Cup{n: 7}, Mug{n: 1}]
    io.print(hs |> Iter.map(&Holder.hold(&1, "ab")) |> Iter.to_list())
end
`
	verifyLambdaProgram(t, src, "cup extra\nholder extra\n[9, 2]\n")
}

// A defaulted parameter before the receiver: the call passes the receiver
// after the slots before the default, so it has its operand to dispatch on.
// P inherits the interface's default, Q declares its own.
func TestIfaceDefault_DispatchedDefaultBeforeTheReceiver(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::io

fn note(label: String, s: String) -> String
    io.print(label)
    s
end

interface Tagger
    fn side(value: self) -> Int

    open fn tag(label: String = note("tagger label", "x"), target: self) -> String
        "${label}${Tagger.side(target)}"
    end
end

struct P
    n: Int
end

impl Tagger for P
    fn side(p: P) -> Int
        p.n
    end
end

struct Q
    n: Int
end

impl Tagger for Q
    fn side(q: Q) -> Int
        q.n
    end

    fn tag(label: String = note("q label", "q"), target: Q) -> String
        "${label}/${target.n}"
    end
end

fn main()
    ts: List<Tagger> = [P{n: 1}, Q{n: 3}]
    io.print(ts |> Iter.map(fn x -> Tagger.tag(target: x)) |> Iter.to_list())
end
`
	verifyLambdaProgram(t, src, "tagger label\nq label\n[x1, q/3]\n")
}
