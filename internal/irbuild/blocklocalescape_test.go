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
	const src = `import std/io

fn main() {
    a = {
        struct Loc {
            n: Int
        }
        Loc{n: 1}
    }
    b = {
        struct Loc {
            s: String
            m: Int
        }
        Loc{s: "x", m: 2}
    }
    io.inspect(a)
    io.inspect(b)
    io.inspect(a.n + b.m)
    io.inspect(a == a)
    e = {
        enum Mode {
            Fast
            Slow(Int)
        }
        [Mode.Fast, Mode.Slow(3)]
    }
    io.inspect(e)
    d = {
        type Meters Int
        Meters(4)
    }
    io.inspect(d)
    make = || {
        struct P {
            x: Int
        }
        P{x: 9}
    }
    io.inspect(make().x)
    xs = Iter.map([1, 2], |i| {
        struct Q {
            i: Int
        }
        Q{i: i}
    }) |> Iter.to_list()
    io.inspect(xs)
    w = {
        struct Box<T> {
            v: T
        }
        Box{v: 1}
    }
    io.inspect(w.v + 1)
    u = {
        struct Box<T> {
            first: T
            second: T
        }
        Box{first: "a", second: "b"}
    }
    io.inspect(u)
    o = {
        enum Opt<T> {
            Has(T)
            Empty
        }
        [Opt.Has(2.5), Opt.Empty]
    }
    io.inspect(o)
}
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
