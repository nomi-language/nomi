package irbuild

import "testing"

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

// A distinct whose inner reaches the distinct back runs: `type Link Node`
// where Node holds a `Maybe<Link>`, `type B W` where W holds a `Maybe<B>`,
// a distinct reached through a struct's List, and one an enum's variant
// carries. The distinct and the types it reaches are decided together,
// co-inductively (irDistinctCoinductive), as a recursive struct is. Each is
// built, destructured, matched, compared and rendered.
func TestIRDistinctOverNominalType_CycleRuns(t *testing.T) {
	const src = `import std/io

struct Node {
    v: Int
    next: Maybe<Link>
}

type Link Node

struct W {
    b: Maybe<B>
}

type B W

struct Tree {
    kids: List<Branch>
}

type Branch Tree

enum Expr {
    Lit(Int)
    Neg(Boxed)
}

type Boxed Expr

fn total(n: Node): Int {
    case n.next {
        Some(Link(m)) -> n.v + total(m)
        None -> n.v
    }
}

fn main() {
    chain = Node{v: 1, next: Some(Link(Node{v: 2, next: None}))}
    io.print(total(chain))
    io.inspect(chain)
    inner = B(W{b: None})
    B(w) = B(W{b: Some(inner)})
    case w.b {
        Some(_) -> io.print("nested")
        None -> io.print("flat")
    }
    _ = dbg w
    io.print(inner == B(W{b: None}))
    io.print(B(w) == inner)
    t = Tree{kids: [Branch(Tree{kids: []})]}
    io.inspect(t)
    io.print(t == Tree{kids: [Branch(Tree{kids: []})]})
    e = Expr.Neg(Boxed(Expr.Lit(3)))
    io.inspect(e)
    io.print(e == Expr.Neg(Boxed(Expr.Lit(3))))
}
`
	want := "3\n" +
		"Node{v: 1, next: Some(Link(Node{v: 2, next: None}))}\n" +
		"nested\n" +
		"dbg line 46: w = W{b: Some(B(W{b: None}))}\n" +
		"True\n" +
		"False\n" +
		"Tree{kids: [Branch(Tree{kids: []})]}\n" +
		"True\n" +
		"Neg(Boxed(Lit(3)))\n" +
		"True\n"
	irRunSource(t, src, want)
}
