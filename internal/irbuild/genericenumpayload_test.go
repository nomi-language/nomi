package irbuild

import "testing"

// A user enum's variant over a payload nested two deep runs: a generic
// enum's positional and struct-shaped variants at `Maybe<Maybe<Int>>`, at a
// tuple holding a user enum and at `Maybe<Result<Int, String>>`, matched,
// compared and shown; and a non-generic enum declaring such payloads and a
// struct-shaped variant holding a function in a tuple.
func TestIRVariantDeepPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

enum Opt<T> {
    Has T
    Nothing
}

derive Equatable for Opt<T>

enum Holder<T> {
    Of {value: T}
    Empty
}

enum Shape {
    Dot
    Circle Int
}

enum Plain {
    Deep Maybe<Maybe<Int>>
    Pair (Shape, String)
    Polygon {sides: (Int, (Int) -> Int)}
}

fn main() {
    a = Opt.Has(Some(Some(1)))
    case a {
        .Has(Some(Some(n))) -> io.inspect(n)
        .Has(_) -> io.inspect(0)
        .Nothing -> io.inspect(-1)
    }
    io.inspect(a == Opt.Has(Some(Some(1))))
    io.inspect(a == Opt.Has(Some(None)))
    io.inspect(Opt.Has((Shape.Dot, "")))
    r = Ok<Int, String>(3)
    io.inspect(Opt.Has(Some(r)))
    io.inspect(Opt.Has(Opt.Has(Some(2))))
    io.inspect(Holder.Of{value: Some(Some(1))})
    case Holder.Of{value: (Shape.Circle(4), [Some("a")])} {
        .Of{value} -> io.inspect(value)
        .Empty -> io.inspect(0)
    }
    io.inspect(Plain.Deep(Some(None)))
    io.inspect(Plain.Pair((Shape.Dot, "p")))
    case Plain.Polygon{sides: (3, |x: Int| x * 2)} {
        .Polygon{sides: (n, f)} -> io.inspect(f(n))
        _ -> io.inspect(0)
    }
}
`
	const want = "1\n" +
		"True\n" +
		"False\n" +
		"Has((Dot, \"\"))\n" +
		"Has(Some(Ok(3)))\n" +
		"Has(Has(Some(2)))\n" +
		"Of{value: Some(Some(1))}\n" +
		"(Circle(4), [Some(\"a\")])\n" +
		"Deep(Some(None))\n" +
		"Pair(Dot, \"p\")\n" +
		"6\n"
	irRunSource(t, src, want)
}
