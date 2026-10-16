package irbuild

import "testing"

// A struct field may hold a Range: written in a literal, as a generic
// struct's field, as a field default, destructured, compared, hashed, and
// carried in an enum payload.
func TestRangeStructField_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

struct Hold {
    v: Range<Int>
}

struct Gbox<A> {
    v: A
}

struct D {
    v: Range<Int> = 2..=5
    n: Int = 0
}

struct Spans {
    f: Range<Float>
    s: Range<String>
}

enum E {
    A Spans
    B
}

fn main() {
    h = Hold{v: 1..4}
    io.inspect(h.v)
    io.inspect(h)
    io.inspect(h == Hold{v: 1..4})
    io.inspect(h.v |> Iter.to_list())
    g = Gbox{v: 1..3}
    io.inspect(g)
    io.inspect(D{})
    io.inspect(D{n: 1, v: 0..1}.v)
    Hold{v: r} = h
    io.inspect(r)
    sp = Spans{f: 1.0..2.0, s: "a"..="c"}
    io.inspect(#{sp} |> Set.size())
    io.print(case E.A(sp) {
        E.A(Spans{f, s}) -> "${Debug.inspect(f)} ${Debug.inspect(s)}"
        E.B -> "b"
    })
}
`, "1..4\nHold{v: 1..4}\nTrue\n[1, 2, 3]\nGbox{v: 1..3}\nD{v: 2..=5, n: 0}\n0..1\n1..4\n1\n1.0..2.0 \"a\"..=\"c\"\n")
}
