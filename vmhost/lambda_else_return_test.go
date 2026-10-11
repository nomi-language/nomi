package vmhost_test

import (
	"strings"
	"testing"
)

// A bare `return` in a lambda leaves the lambda wherever it ends a path: the
// else of a binding whose pattern falls back to a payload
// (`Some(v) = m else { return }`, as a block or an arm), an `if` guard, a
// `case` arm. The fallback else is a region of the payload's kind, not of the
// lambda's Unit result, and the builder declined the bare `return` there.
// Each shape runs in an Iter.each, Iter.map, Iter.filter, Iter.loop or
// Maybe.map callback, a nested lambda, or a lambda bound to a name and
// called.
func TestLambdaElseReturn_RunsInEveryCallback(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn a() {
    Iter.each([1.5, -2.0], |f| {
        Some(d) = Decimal.from_float(f, 1, .HalfUp) else { return Unit }
        io.print("a ${d}")
    })
}

fn b() {
    xs = [Some(1), None, Some(3)]
        |> Iter.map(|m| {
            Some(v) = m else { return 0 }
            v * 10
        })
        |> Iter.to_list()
    io.print("b ${xs}")
}

fn c() {
    Iter.each([1, 2, 3], |n| {
        if n == 2 {
            io.print("c skip")
            return
        }
        io.print("c ${n}")
    })
}

fn d() {
    Iter.each([[Some(1), None], [Some(2)]], |row| {
        Iter.each(row, |m| {
            Some(v) = m else { return }
            io.print("d ${v}")
        })
    })
}

fn e() {
    show = |m: Maybe<Int>| {
        Some(v) = m else { return }
        io.print("e ${v}")
    }
    show(Some(7))
    show(None)
}

fn f() {
    ys = [Some(1), None, Some(4)]
        |> Iter.filter(|m| {
            Some(v) = m else { return False }
            v > 2
        })
        |> Iter.count()
    io.print("f ${ys}")
}

fn g() {
    z = Maybe.map(Some(Some(5)), |m| {
        Some(v) = m else { return 0 }
        v + 1
    })
    io.print("g ${z}")
}

fn h() {
    r = Iter.loop(|i = 0| {
        if i >= 4 {
            break i
        }
        Some(v) = (if i % 2 == 0 { Some(i) } else { None }) else { return i + 1 }
        io.print("h ${v}")
        i + 1
    })
    io.print("h done ${r}")
}

fn i() {
    Iter.each([Some(1), None], |m| {
        case m {
            Some(v) -> io.print("i ${v}")
            None -> return
        }
        io.print("i after")
    })
}

fn j() {
    Iter.each([Some(1), None], |m| {
        Some(v) = m else {
            io.print("j none")
            return
        }
        io.print("j ${v}")
    })
}

fn k() {
    Iter.each([Ok(1), Err("x")], |r| {
        Ok(v) = r else {
            Err(e) -> {
                io.print("k ${e}")
                return
            }
        }
        io.print("k ${v}")
    })
}

fn main() {
    a()
    b()
    c()
    d()
    e()
    f()
    g()
    h()
    i()
    j()
    k()
}
`)
	want := strings.Join([]string{
		"a 1.5", "a -2.0",
		"b [10, 0, 30]",
		"c 1", "c skip", "c 3",
		"d 1", "d 2",
		"e 7",
		"f 1",
		"g Some(6)",
		"h 0", "h 2", "h done 4",
		"i 1", "i after",
		"j 1", "j none",
		"k 1", "k x",
	}, "\n") + "\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// The reported program: a Decimal conversion whose else leaves the Iter.each
// callback.
func TestLambdaElseReturn_DecimalFromFloat(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn main() {
    Iter.each([212.0, 33.8, 2.675, -0.05], |f| {
        Some(d) = Decimal.from_float(f, 1, .HalfUp) else { return }
        io.print("${f} -> ${d}")
    })
}
`)
	want := "212.0 -> 212.0\n33.8 -> 33.8\n2.675 -> 2.7\n-0.05 -> -0.1\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
