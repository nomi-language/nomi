package irbuild

import "testing"

// A generic struct or enum declared in a body runs: built by literal, by
// call form and by variant, nested in itself, holding a block-local distinct
// and another block-local generic, matched, shown by its universal Debug,
// built in a lambda, and declared under the same name with other fields in
// another function.
func TestIRBlockLocalGenericType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn other(): String {
    struct Box<T> {
        label: String
        v: T
    }
    b = Box{label: "o", v: [1]}
    "${b.label}:${Iter.count(b.v)}"
}

fn main() {
    type Meters Int
    struct Box<T> {
        v: T
    }
    enum Opt<T> {
        Has T
        Nothing
    }
    struct Tagged<T> {
        m: Meters
        o: Opt<T>
    }
    io.inspect(Box{v: "s"})
    io.inspect(Box({v: 1.5}))
    io.inspect(Box{v: Box{v: 2}}.v.v)
    case Opt.Has(Meters(3)) {
        .Has(Meters(n)) -> io.inspect(n)
        .Nothing -> io.inspect(0)
    }
    io.inspect(Tagged{m: Meters(1), o: Opt.Has(True)})
    io.inspect(Iter.map([1, 2], |x| Box{v: x}) |> Iter.to_list())
    io.inspect([Opt.Has(1), Opt.Nothing])
    io.print(other())
}
`
	const want = "Box{v: \"s\"}\n" +
		"Box{v: 1.5}\n" +
		"2\n" +
		"3\n" +
		"Tagged{m: Meters(1), o: Has(True)}\n" +
		"[Box{v: 1}, Box{v: 2}]\n" +
		"[Has(1), Nothing]\n" +
		"o:1\n"
	irRunSource(t, src, want)
}
