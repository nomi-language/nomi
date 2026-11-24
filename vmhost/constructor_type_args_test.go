package vmhost_test

import (
	"testing"
)

// A construction's type arguments name the instance it builds, and every
// form builds and runs at it: a generic distinct's call form, written and
// piped, one whose argument names no type (`Ids<Int>([])`), a struct's call
// form with a defaulted parameter left out, a struct-shaped variant's record
// form, written and piped, a prelude variant whose enum's parameters the
// payload names out of order, and a generic body's own parameter as the
// argument. An operator interface's function takes its implementor as its
// type argument, written, piped and in a partial. `Tagged<String>(...)` and
// `Ids<Int>([])` were "not locally determined", since a construction ignored
// its type arguments.
func TestCallTypeArgs_ConstructionsRunAtTheInstanceTheyName(t *testing.T) {
	got := runSourceOutput(t, `type Tagged<T> (String, T)

type Ids<T> List<T>

struct Box<T>
    v: T
end

struct Pair<A, B = String>
    a: A
    b: B
end

enum Holder<T>
    Of T
    Rec {value: T}
end

fn boxed<T>(x: T) -> Box<T>
    Box<T>({v: x})
end

fn main()
    t = Tagged<String>(("n", "x"))
    ids = Ids<Int>([])
    p = Pair<Int>({a: 1, b: "two"})
    h = Holder.Rec<Int>({value: 1})
    e = Err<Int, String>("bad")
    f = &Add.add<Float>(&1, 2.0)
    dbg t
    dbg ids
    dbg p
    dbg h
    dbg e
    dbg ("a", 1) |> Tagged<Int>()
    dbg {value: 3} |> Holder.Rec<Int>()
    dbg {v: 2} |> Box<Int>()
    dbg boxed(4)
    dbg Add.add<Float>(1.5, 2.5)
    dbg 1.5 |> Add.add<Float>(2.5)
    dbg f(1.0)
end
`)
	want := `dbg line 30: t = Tagged("n", "x")
dbg line 31: ids = Ids([])
dbg line 32: p = Pair{a: 1, b: "two"}
dbg line 33: h = Rec{value: 1}
dbg line 34: e = Err("bad")
dbg line 35: ("a", 1) |> Tagged<Int>() = Tagged("a", 1)
dbg line 36: {value: 3} |> Holder.Rec<Int>() = Rec{value: 3}
dbg line 37: {v: 2} |> Box<Int>() = Box{v: 2}
dbg line 38: boxed(4) = Box{v: 4}
dbg line 39: Add.add<Float>(1.5, 2.5) = 4.0
dbg line 40: 1.5 |> Add.add<Float>(2.5) = 4.0
dbg line 41: f(1.0) = 3.0
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
