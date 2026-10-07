package irbuild

import (
	"strings"
	"testing"
)

// A call through a function value read by a field chain of any length,
// through struct fields, tuple indices and a call's result, runs as the same
// call through a binding of that value does.
func TestIRFieldChainCall_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Q {
    f: (Int) -> Int
}

struct P {
    t: ((Int) -> Int, Int)
    q: Q
    qs: (Int, (Q, Int))
}

fn make(): P {
    P{t: (|x| x * 10, 2), q: Q{f: |x| x - 1}, qs: (0, (Q{f: |x| x * x}, 3))}
}

fn main() {
    p = P{t: (|x| x + 1, 1), q: Q{f: |x| x + 100}, qs: (0, (Q{f: |x| x + 7}, 3))}
    io.inspect(p.t.0(8))
    io.inspect(p.q.f(8))
    io.inspect((p.qs.1).0.f(8))
    io.inspect(make().t.0(8))
    io.inspect((make().qs.1).0.f(5))
    io.inspect(8 |> p.t.0())
    pair = (p, 4)
    io.inspect(pair.0.q.f(pair.1))
}
`
	const want = "9\n108\n15\n80\n25\n9\n104\n"
	irRunSource(t, src, want)
}

// In an assertion subject the call shows its arguments and its result as
// a call through a binding does.
func TestIRFieldChainCall_AssertionRows(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `struct Q {
    f: (Int) -> Int
}

struct P {
    t: ((Int) -> Int, Int)
    q: Q
}

test "passes" {
    p = P{t: (|x| x + 1, 1), q: Q{f: |x| x + 100}}
    assert p.t.0(8) == 9
    assert p.q.f(p.t.0(1)) == 102
}

test "fails" {
    p = P{t: (|x| x + 1, 1), q: Q{f: |x| x + 100}}
    assert p.q.f(2) == 3
}
`
	out := irTestBodyVM(t, src, 2)
	want := "    assert p.q.f(2) == 3\n    values:\n      p.q.f(2)\n        = 102\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want the failing case's rows\n%s\ngot:\n%s", want, out)
	}
}
