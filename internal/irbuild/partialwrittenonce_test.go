package irbuild

import "testing"

// A partial capture evaluates each written argument once per call, and a
// default the call leaves out runs after the written arguments and reads
// their values (spec §7 *Captures*, §7 *Default Arguments*). Each program
// below leaves out an open slot whose default is computed, so the call goes
// through the partial's entry, whose suppliers evaluate the written
// argument and then the default; `say` printing twice per call is the
// written argument evaluated again for the default.

const partialWrittenOnceSay = `fn say(n: Int) -> Int
    io.print("say ${n}")
    n
end
`

// Over a function of this file, whose default reads the written argument,
// and one whose default reads none, which still runs after it; and with the
// written argument after the open slot, which runs before that slot's
// default.
func TestPartialWrittenOnce_ModuleFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

`+partialWrittenOnceSay+`
fn base() -> Int
    io.print("base")
    7
end

fn f(a: Int, b: Int, c: Int = a * 2) -> Int
    a + b + c
end

fn g(a: Int, b: Int, c: Int = base()) -> Int
    a + b + c
end

fn first(a: Int = base(), b: Int) -> Int
    a + b
end

fn main()
    h = &f(say(1), &1, &2)
    io.print("${h(10)}")
    io.print("${h(20, 0)}")
    k = &g(say(2), &1, &2)
    io.print("${k(10)}")
    p = &first(&1, b: say(3))
    io.print("${p()}")
end
`, "say 1\n13\nsay 1\n21\nsay 2\nbase\n19\nsay 3\nbase\n10\n")
}

// Over a nested `fn`.
func TestPartialWrittenOnce_NestedFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

`+partialWrittenOnceSay+`
fn main()
    fn nested(a: Int, b: Int, c: Int = a * 5) -> Int
        a + b + c
    end
    n = &nested(say(2), &1, &2)
    io.print("${n(10)}")
end
`, "say 2\n22\n")
}

// Over an owner-qualified impl function.
func TestPartialWrittenOnce_QualifiedImplFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

`+partialWrittenOnceSay+`
struct Box
    v: Int
end

impl Box
    fn g(x: Box, a: Int, b: Int, c: Int = a * 3) -> Int
        x.v + a + b + c
    end
end

fn main()
    q = &Box.g(Box{v: 100}, say(3), &1, &2)
    io.print("${q(10)}")
end
`, "say 3\n122\n")
}

// Over a generic function, whose defaults read the written argument and
// call `say` themselves.
func TestPartialWrittenOnce_GenericFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

`+partialWrittenOnceSay+`
fn pick<T>(a: T, b: T = a, n: Int = say(7)) -> (T, T, Int)
    (a, b, n)
end

fn main()
    p = &pick(say(1), &1, &2)
    io.inspect(p(5))
end
`, "say 1\nsay 7\n(1, 5, 7)\n")
}

// Over a std function whose open slot's default is computed:
// NaiveDateTime.from_instant's offset.
func TestPartialWrittenOnce_StdFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import
    std::io
    std::calendar.NaiveDateTime
    std::instant.Instant
    std::duration.Duration
end

`+partialWrittenOnceSay+`
fn main()
    s = &NaiveDateTime.from_instant(Instant.from_seconds(say(0)), &1)
    io.print("${s()}")
    io.print("${s(Duration.hours(1))}")
end
`, "say 0\n1970-01-01T00:00:00\nsay 0\n1970-01-01T01:00:00\n")
}

// The control: an open slot whose default is a literal, which needs no
// supplier, and an unmentioned computed one, which the body's call fills.
func TestPartialWrittenOnce_LiteralDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

`+partialWrittenOnceSay+`
fn f(a: Int, b: Int, c: Int = a * 2) -> Int
    a + b + c
end

fn g(a: Int, b: Int, c: Int = 4) -> Int
    a + b + c
end

fn main()
    k = &g(say(1), &1, &2)
    io.print("${k(10)}")
    u = &f(say(2), &1)
    io.print("${u(10)}")
end
`, "say 1\n15\nsay 2\n16\n")
}
