package irbuild

import "testing"

// A call of an interface function whose receiver's type is known where it
// is lowered reaches one impl function, and each parameter it leaves out
// takes that function's default when it declares one and the interface's
// otherwise (spec §12 *Item kinds*). Ring declares `scale` and inherits
// `tail`, whose interface default reads the scale Ring's default made.

// ifaceSlotsRing implements Shape (ifaceDispatchShape) for Ring, whose
// `area` declares a default for `scale` only, through `mark`, private to
// the file that declares it.
const ifaceSlotsRing = `fn mark(label: String, n: Int) -> Int
    io.print(label)
    n
end

pub struct Ring
    r: Int
end

impl Shape for Ring
    fn side(g: Ring) -> Int
        g.r
    end

    fn area(g: Ring, scale: Int = mark("ring scale", g.r * 10), tail: Int) -> Int
        g.r * scale + tail
    end
end
`

// ifaceSlotsCalls call Ring's `area` every way that leaves a defaulted
// parameter out, the last on a Shape value.
const ifaceSlotsCalls = `fn main()
    ring = Ring{r: 2}
    io.print("qualified ${Shape.area(ring)}")
    io.print("piped ${ring |> Shape.area()}")
    io.print("named ${Shape.area(value: ring)}")
    io.print("named after ${Shape.area(value: ring, tail: written(1))}")
    io.print("type parameter ${via(ring)}")
    f = &Shape.area(&1)
    io.print("partial ${f(ring)}")
    h = &Shape.area(&1, &2)
    io.print("partial open ${h(ring)}")
    s: Shape = ring
    io.print("erased ${Shape.area(s)}")
end
`

// Each call takes Ring's scale, 20, and the interface's tail, 20 + 2.
const ifaceSlotsWant = "ring scale\nshape tail\nqualified 62\n" +
	"ring scale\nshape tail\npiped 62\n" +
	"ring scale\nshape tail\nnamed 62\n" +
	"written\nring scale\nnamed after 41\n" +
	"ring scale\nshape tail\ntype parameter 62\n" +
	"ring scale\nshape tail\npartial 62\n" +
	"ring scale\nshape tail\npartial open 62\n" +
	"ring scale\nshape tail\nerased 62\n"

func TestIfaceDefault_KnownReceiverTakesEachSlotsDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+ifaceDispatchShape+"\n"+ifaceSlotsRing+"\n"+ifaceDispatchHelpers+"\n"+ifaceSlotsCalls, ifaceSlotsWant)
	})
	t.Run("the interface and the impl in other files", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport ring.Ring\n\n"+ifaceDispatchHelpers+"\n"+ifaceSlotsCalls, ifaceSlotsWant,
			map[string]string{
				"shape.nomi": "import std::io\n\n" + ifaceDispatchShape,
				"ring.nomi":  "import std::io\nimport shape.Shape\n\n" + ifaceSlotsRing,
			})
	})
}

// ifaceSlotsTagger has a defaulted parameter before the receiver. P
// inherits the interface's function; Q declares its own default, through
// `mark`, private to its file.
const ifaceSlotsTagger = `fn note(label: String, s: String) -> String
    io.print(label)
    s
end

pub interface Tagger
    fn side(value: self) -> Int

    open fn tag(label: String = note("tagger label", "x"), target: self) -> String
        "${label}${Tagger.side(target)}"
    end
end
`

const ifaceSlotsTags = `fn mark(label: String, s: String) -> String
    io.print(label)
    s
end

pub struct P
    n: Int
end

impl Tagger for P
    fn side(p: P) -> Int
        p.n
    end
end

pub struct Q
    n: Int
end

impl Tagger for Q
    fn side(q: Q) -> Int
        q.n
    end

    fn tag(label: String = mark("q label", "q"), target: Q) -> String
        "${label}/${target.n}"
    end
end
`

const ifaceSlotsTagCalls = `fn main()
    p = P{n: 1}
    q = Q{n: 3}
    io.print("q named ${Tagger.tag(target: q)}")
    io.print("q direct ${Q.tag(target: q)}")
    io.print("p named ${Tagger.tag(target: p)}")
    f = &Tagger.tag(target: &1)
    io.print("q partial ${f(q)}")
end
`

const ifaceSlotsTagWant = "q label\nq named q/3\nq label\nq direct q/3\n" +
	"tagger label\np named x1\nq label\nq partial q/3\n"

// A slot before the receiver, left out of a call whose receiver's type is
// known: the impl's accessor for it takes the receiver too, called
// directly.
func TestIfaceDefault_KnownReceiverDefaultBeforeTheReceiver(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+ifaceSlotsTagger+"\n"+ifaceSlotsTags+"\n"+ifaceSlotsTagCalls, ifaceSlotsTagWant)
	})
	t.Run("the interface and the impls in other files", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport tagger.Tagger\nimport tags.{P, Q}\n\n"+ifaceSlotsTagCalls, ifaceSlotsTagWant,
			map[string]string{
				"tagger.nomi": "import std::io\n\n" + ifaceSlotsTagger,
				"tags.nomi":   "import std::io\nimport tagger.Tagger\n\n" + ifaceSlotsTags,
			})
	})
}

// ifaceSlotsOpenCalls make partials that leave `scale` open, on a Circle
// and on a Shape value; a call of either that leaves it out takes
// Circle's default, by the impl's accessor or by dispatch, and Dot's is
// the interface's.
const ifaceSlotsOpenCalls = `fn main()
    c = Circle{r: 2}
    h = &Shape.area(&1, &2)
    io.print("partial open ${h(c)}")
    io.print("partial open written ${h(c, written(3))}")
    s: Shape = c
    g = &Shape.area(&1, &2)
    io.print("partial open on a Shape ${g(s)}")
    d: Shape = Dot{n: 3}
    io.print("partial open on a Dot ${g(d)}")
end
`

const ifaceSlotsOpenWant = ifaceDispatchCircle + "partial open 80\n" +
	"written\ncircle tail\npartial open written 12\n" +
	ifaceDispatchCircle + "partial open on a Shape 80\n" +
	"shape scale\nshape tail\npartial open on a Dot 7\n"

// A partial that leaves a defaulted parameter of an interface function
// open: a call of it that leaves the argument out takes the default of the
// impl function that runs.
func TestIfaceDefault_PartialWithTheDefaultedSlotOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+ifaceDispatchShape+"\n"+ifaceDispatchImpls+"\n"+ifaceDispatchHelpers+"\n"+ifaceSlotsOpenCalls, ifaceSlotsOpenWant)
	})
	t.Run("the interface and its impls in other files", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport impls.{Circle, Dot}\n\n"+ifaceDispatchHelpers+"\n"+ifaceSlotsOpenCalls, ifaceSlotsOpenWant,
			map[string]string{
				"shape.nomi": "import std::io\n\n" + ifaceDispatchShape,
				"impls.nomi": "import std::io\nimport shape.Shape\n\n" + ifaceDispatchImpls,
			})
	})
	// The interface's default is a literal, which a partial would carry as
	// its own parameter's default; Circle's replaces it.
	t.Run("a literal interface default", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io

fn note(label: String, n: Int) -> Int
    io.print(label)
    n
end

interface Shape
    fn side(value: self) -> Int

    open fn area(value: self, scale: Int = 1) -> Int
        Shape.side(value) * scale
    end
end

struct Circle
    r: Int
end

impl Shape for Circle
    fn side(c: Circle) -> Int
        c.r
    end

    fn area(c: Circle, scale: Int = note("circle", 100)) -> Int
        c.r * scale
    end
end

fn main()
    c = Circle{r: 2}
    h = &Shape.area(&1, &2)
    io.print("concrete ${h(c)}")
    s: Shape = c
    g = &Shape.area(&1, &2)
    io.print("on a Shape ${g(s)}")
end
`, "circle\nconcrete 200\ncircle\non a Shape 200\n")
	})
}
