package vmhost_test

import "testing"

// embedsShapeSrc declares an `embeds` enum with a written Display and a
// written Debug, a Circle with a derived Display and its shape for Debug,
// a struct and an enum that derive Display over a Shape position, and a
// function that widens a Circle into a Shape: a Shape that holds a Circle
// is the Circle at run time.
const embedsShapeSrc = `import std::io

struct Circle
    r: Int
end

derive Display for Circle

enum Shape
    embeds Circle
    Dot
end

impl Display for Shape
    fn to_string(s: Shape) -> String
        case s
            .Circle{r:} -> "shape ${r}"
            .Dot -> "dot"
        end
    end
end

impl Debug for Shape
    fn inspect(s: Shape) -> String
        case s
            .Circle{r:} -> "dshape ${r}"
            .Dot -> "ddot"
        end
    end
end

struct Holder
    s: Shape
end

derive Display for Holder

enum Wrap
    W(Shape)
end

derive Display for Wrap

fn shape(r: Int) -> Shape
    Circle{r:}
end

fn show(d: Display) -> String
    "${d}"
end
`

// A rendering by shape picks each part's body by the static type of its
// position, so a Circle that a Shape holds renders through Shape's written
// Display and Debug wherever the position is a Shape (an Option, a Result,
// a tuple's slot, a struct's field, a variant's payload, a List's element,
// a Map's value), a Circle beside it in the same rendering renders as a
// Circle (one in a List of Shapes is a Shape), and a value held as an interface value renders as its static type
// says, through a thunk at that type.
func TestEmbedsEnum_AWrittenImplRendersItsEmbeddedValuesByPosition(t *testing.T) {
	got := runSourceOutput(t, embedsShapeSrc+`
fn main()
    c = Circle{r: 0}
    r: Result<Shape, String> = Ok(shape(4))
    io.print("${Some(shape(1))} ${r} ${(shape(2), c)} ${Holder{s: shape(3)}} ${Wrap.W(shape(5))} ${[shape(6), c]} ${{"k" => shape(7)}}")
    io.print("${Some((c, shape(8)))} ${Some(c)}")
    io.print(Debug.inspect(Some(shape(1))) + " " + Debug.inspect(r) + " " + Debug.inspect((shape(2), c)) + " " + Debug.inspect(Holder{s: shape(3)}) + " " + Debug.inspect(Wrap.W(shape(5))) + " " + Debug.inspect([shape(6)]) + " " + Debug.inspect({"k" => shape(7)}))
    m: Display = Some(shape(9))
    hd: Display = Holder{s: shape(10)}
    tp: Display = (shape(11), 1)
    io.print("${show(m)} ${hd} ${tp}")
    g: Debug = Some(shape(12))
    gs: List<Debug> = [(shape(13), 1), [shape(14)], Holder{s: shape(15)}]
    io.print(Debug.inspect(g) + " " + Debug.inspect(gs))
end
`)
	want := `Some(shape 1) Ok(shape 4) (shape 2, Circle{r: 0}) Holder{s: shape 3} W(shape 5) [shape 6, shape 0] {k => shape 7}
Some((Circle{r: 0}, shape 8)) Some(Circle{r: 0})
Some(dshape 1) Ok(dshape 4) (dshape 2, Circle{r: 0}) Holder{s: dshape 3} W(dshape 5) [dshape 6] {"k" => dshape 7}
Some(shape 9) Holder{s: shape 10} (shape 11, 1)
Some(dshape 12) [(dshape 13, 1), [dshape 14], Holder{s: dshape 15}]
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
