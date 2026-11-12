package irbuild

import "testing"

// A value of a block-local type runs outside the block that declares it,
// where the type's name no longer resolves: a struct, two blocks' structs
// of one name with different fields, an enum, a distinct, a lambda's
// result, an Iter.map callback's, and generic structs and enums, read,
// compared and inspected.
func TestIRBlockLocalTypeEscapesItsBlock_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    a = begin
        struct Loc
            n: Int
        end
        Loc{n: 1}
    end
    b = begin
        struct Loc
            s: String
            m: Int
        end
        Loc{s: "x", m: 2}
    end
    io.inspect(a)
    io.inspect(b)
    io.inspect(a.n + b.m)
    io.inspect(a == a)
    e = begin
        enum Mode
            Fast
            Slow(Int)
        end
        [Mode.Fast, Mode.Slow(3)]
    end
    io.inspect(e)
    d = begin
        type Meters Int
        Meters(4)
    end
    io.inspect(d)
    make = fn ->
        struct P
            x: Int
        end
        P{x: 9}
    end
    io.inspect(make().x)
    xs = Iter.map([1, 2], fn i ->
        struct Q
            i: Int
        end
        Q{i:}
    end) |> Iter.to_list()
    io.inspect(xs)
    w = begin
        struct Box<T>
            v: T
        end
        Box{v: 1}
    end
    io.inspect(w.v + 1)
    u = begin
        struct Box<T>
            first: T
            second: T
        end
        Box{first: "a", second: "b"}
    end
    io.inspect(u)
    o = begin
        enum Opt<T>
            Has(T)
            Empty
        end
        [Opt.Has(2.5), Opt.Empty]
    end
    io.inspect(o)
end
`
	const want = "Loc{n: 1}\n" +
		"Loc{s: \"x\", m: 2}\n" +
		"3\n" +
		"True\n" +
		"[Fast, Slow(3)]\n" +
		"Meters(4)\n" +
		"9\n" +
		"[Q{i: 1}, Q{i: 2}]\n" +
		"2\n" +
		"Box{first: \"a\", second: \"b\"}\n" +
		"[Has(2.5), Empty]\n"
	irRunSource(t, src, want)
}
