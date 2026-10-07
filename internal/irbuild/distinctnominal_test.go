package irbuild

import (
	"strings"
	"testing"
)

// A distinct over a declared type runs: over a struct, an enum (Maybe and
// Result included), another distinct, a std Set or Vector, and a list of
// declared values. Each is built, destructured in a binding, a parameter
// and a `case`, shown, compared with `==`, held in a struct field and a
// list, used as a map key and as a generic type argument, and declared in
// a block.
func TestIRDistinctOverNominalType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

type Meters Int

type Twice Meters

type Wrapped Maybe<Int>

type Res Result<Int, String>

struct P {
    x: Int
}

type WP P

type Twice2 WP

enum Color {
    Red
    Green
}

type C Color

type Ids Set<Int>

type Vs Vector<Int>

type Ms List<Meters>

struct H {
    p: WP
    w: Wrapped
}

impl Display for WP {
    fn to_string(w: WP): String {
        WP(p) = w
        "wp ${p.x}"
    }
}

fn first<T>(xs: List<T>): Maybe<T> {
    List.head(xs)
}

fn get(w: Wrapped): Int {
    case w {
        Wrapped(Some(v)) -> v
        Wrapped(None) -> 0
    }
}

fn unwrap(Wrapped(m): Wrapped): Maybe<Int> {
    m
}

fn main() {
    io.inspect(Wrapped(Some(1)))
    io.inspect(Wrapped(None))
    io.inspect(unwrap(Wrapped(Some(5))))
    io.inspect(get(Wrapped(Some(9))))
    io.inspect(get(Wrapped(None)))
    io.inspect(Wrapped(Some(1)) == Wrapped(Some(1)))
    io.inspect(Wrapped(Some(1)) == Wrapped(Some(2)))
    io.inspect(Res(Err("no")))
    t = Twice(Meters(3))
    io.inspect(t)
    Twice(m) = t
    io.inspect(Int(m))
    io.inspect(Twice2(WP(P{x: 1})))
    Twice2(WP(p)) = Twice2(WP(P{x: 7}))
    io.inspect(p.x)
    io.inspect(C(Color.Green))
    io.print(Display.to_string(WP(P{x: 4})))
    io.inspect(Ids(#{1, 2}) == Ids(#{2, 1}))
    io.inspect(Vs(#[1, 2]))
    io.inspect(Ms([Meters(1)]))
    io.inspect(first([Wrapped(Some(1))]))
    io.inspect([1, 2] |> Iter.map(|n| Wrapped(Some(n))) |> Iter.to_list())
    io.inspect(Map.get({Wrapped(Some(1)) => "a"}, Wrapped(Some(1))))
    h = H{p: WP(P{x: 2}), w: Wrapped(None)}
    io.inspect(h)
    io.inspect(h == H{p: WP(P{x: 2}), w: Wrapped(Some(1))})
    {
        type Local Maybe<String>
        io.inspect(Local(Some("a")))
    }
}
`
	const want = "Wrapped(Some(1))\n" +
		"Wrapped(None)\n" +
		"Some(5)\n" +
		"9\n" +
		"0\n" +
		"True\n" +
		"False\n" +
		"Res(Err(\"no\"))\n" +
		"Twice(Meters(3))\n" +
		"3\n" +
		"Twice2(WP(P{x: 1}))\n" +
		"7\n" +
		"C(Green)\n" +
		"wp 4\n" +
		"True\n" +
		"Vs(#[1, 2])\n" +
		"Ms([Meters(1)])\n" +
		"Some(Wrapped(Some(1)))\n" +
		"[Wrapped(Some(1)), Wrapped(Some(2))]\n" +
		"Some(\"a\")\n" +
		"H{p: WP(P{x: 2}), w: Wrapped(None)}\n" +
		"False\n" +
		"Local(Some(\"a\"))\n"
	irRunSource(t, src, want)
}

// A distinct whose inner reaches the distinct back is declined, not
// recursed into without end: `type Link Node` where Node holds a
// `Maybe<Link>`.
func TestIRDistinctOverNominalType_CycleDeclines(t *testing.T) {
	const src = `import std/io

struct Node {
    v: Int
    next: Maybe<Link>
}

type Link Node

fn main() {
    io.inspect(Node{v: 1, next: Some(Link(Node{v: 2, next: None}))})
}
`
	if _, err := AnalyzeSource("entry.nomi", src); err != nil {
		t.Fatalf("the front end rejects this, so it no longer tests the cycle: %v", err)
	}
	got := vmReference(writeTemp(t, src))
	if got.exit == 0 {
		t.Fatalf("this program runs now; if that is a fix, assert its output instead:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "is not supported yet") {
		t.Fatalf("want a decline, got (exit %d):\n%s", got.exit, got.stderr)
	}
}
