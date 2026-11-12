package irbuild

import "testing"

// A type declared in a body, named in that body's annotations, runs: a
// lambda parameter of it inside a generic variant (`Some(fn x: Meters -> ..)`),
// a nested fn over it passed to a user generic, and binding annotations of
// it and of a block-local generic struct.
func TestIRBlockLocalTypeInAnnotations_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn ident<T>(x: T) -> T
    x
end

fn main()
    type Meters Int
    struct Box<T>
        v: T
    end
    f = Some(fn x: Meters ->
        Meters(n) = x
        n + 1
    end)
    io.inspect(Maybe.is_some(f))
    case f
        Some(g) -> io.inspect(g(Meters(4)))
        None -> io.inspect(0)
    end
    fn len(m: Meters) -> Int
        Meters(n) = m
        n
    end
    h = ident(len)
    io.inspect(h(Meters(3)))
    m: Meters = Meters(7)
    b: Box<Meters> = Box{v: m}
    io.inspect(b)
end
`
	const want = "True\n" +
		"5\n" +
		"3\n" +
		"Box{v: Meters(7)}\n"
	irRunSource(t, src, want)
}
