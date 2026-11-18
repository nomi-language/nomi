package irbuild

import (
	"strings"
	"testing"
)

// ifaceEarlierShape declares an interface default whose parameter `extra`
// defaults to a value read from the earlier parameters `value` and `scale`.
// Each default goes through `note`, which prints its label, so a run shows
// which defaults were evaluated, once each, after the written arguments.
const ifaceEarlierShape = `fn note(label: String, n: Int) -> Int
    io.print(label)
    n
end

pub interface Shape
    fn side(value: self) -> Int

    open fn area(value: self, scale: Int = note("scale", 1), extra: Int = note("extra", scale * 10 + Shape.side(value)), tail: Int = 0) -> Int
        Shape.side(value) * scale + extra + tail
    end
end
`

const ifaceEarlierCircle = `pub struct Circle
    r: Int
end

impl Shape for Circle
    fn side(c: Circle) -> Int
        c.r
    end
end
`

const ifaceEarlierCalls = `fn written(n: Int) -> Int
    io.print("written")
    n
end

fn via<T>(x: T) -> Int where T: Shape
    T.area(x, written(2))
end

fn erased(s: Shape) -> Int
    Shape.area(s, written(2))
end

fn main()
    c = Circle{r: 3}
    io.print("direct ${Circle.area(c, written(2))}")
    io.print("qualified ${Shape.area(c, written(2))}")
    io.print("type parameter ${via(c)}")
    io.print("erased ${erased(c)}")
    s: Shape = c
    io.print("erased binding ${Shape.area(s, written(2))}")
    io.print("piped ${c |> Shape.area(written(2))}")
    io.print("named ${Shape.area(c, tail: written(100))}")
    io.print("named direct ${Circle.area(c, tail: written(100))}")
    f = &Shape.area(&1)
    io.print("partial ${f(c)}")
end
`

const ifaceEarlierWant = "written\nextra\ndirect 29\n" +
	"written\nextra\nqualified 29\n" +
	"written\nextra\ntype parameter 29\n" +
	"written\nextra\nerased 29\n" +
	"written\nextra\nerased binding 29\n" +
	"written\nextra\npiped 29\n" +
	"written\nscale\nextra\nnamed 116\n" +
	"written\nscale\nextra\nnamed direct 116\n" +
	"scale\nextra\npartial 16\n"

// A default of an interface function's parameter reads the parameters
// before it, as a function's does, on every route a call takes: the
// implementing type, the interface, a type parameter, an erased receiver,
// a pipe, a named argument past it and a partial.
func TestIfaceDefault_DefaultReadsAnEarlierParameter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+ifaceEarlierShape+"\n"+ifaceEarlierCircle+"\n"+ifaceEarlierCalls, ifaceEarlierWant)
	})
	// `note` is private to shape.nomi, so a call in main.nomi fills the
	// slot through shape.nomi's accessor, which takes the parameters
	// before it.
	t.Run("the interface in another file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport circle.Circle\n\n"+ifaceEarlierCalls, ifaceEarlierWant,
			map[string]string{
				"shape.nomi":  "import std::io\n\n" + ifaceEarlierShape,
				"circle.nomi": "import shape.Shape\n\n" + ifaceEarlierCircle,
			})
	})
}

// The same defaults, filled in a test's body.
func TestIfaceDefault_DefaultReadsAnEarlierParameterInATest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := "import std::io\n\n" + ifaceEarlierShape + "\n" + ifaceEarlierCircle + `
test "an interface default reads an earlier parameter"
    c = Circle{r: 3}
    assert Shape.area(c, 2) == 29
    assert Circle.area(c) == 16
    s: Shape = c
    assert Shape.area(s, 2, tail: 1) == 30
end
`
	out := irTestBodyVM(t, src, 1)
	want := "  extra\n  scale\n  extra\n  extra\n"
	if !strings.Contains(out, want) || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the case to pass with the defaults' output\n%s\ngot:\n%s", want, out)
	}
}
