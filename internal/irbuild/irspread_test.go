package irbuild

import "testing"

func TestIRSpread_TourStructUpdates(t *testing.T) {
	verifyLambdaProgram(t, `struct User
  name: String
  age: Int
end

fn main()
  alice = User{name: "Alice", age: 30}
  older = {..alice, age: 31}
  dbg older

  point = {x: 10, y: 20}
  dbg {..point, x: 9}

  // Punning works after a spread: `+"`age:`"+` means `+"`age: age`"+`.
  age = 32
  dbg {..alice, age:}
end
`, "dbg line 9: older = User{name: \"Alice\", age: 31}\n"+
		"dbg line 12: {..point, x: 9} = {x: 9, y: 20}\n"+
		"dbg line 16: {..alice, age:} = User{name: \"Alice\", age: 32}\n")
}

// The base evaluates first, then each value in source order; the original is
// untouched, and a later field written twice through updates keeps its own
// value.
func TestIRSpread_OrderAndPersistence(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct P
  x: Int
  y: Int
  label: String
end
fn mark(label: String, n: Int) -> Int
  io.print(label)
  n
end
fn base() -> P
  io.print("base")
  P{x: 1, y: 2, label: "p"}
end
fn main()
  q = {..base(), y: mark("y", 20), x: mark("x", 10)}
  io.print(q.x + q.y)
  r = {..q, label: "r"}
  io.print(r.label)
  io.print(q.label)
end
`, "base\ny\nx\n30\nr\np\n")
}

// A literal with spreads is the kind the checker gave it, not its first
// spread's: a `List<Int>`, a Set, an Option or a Range spread into a List,
// Vector or Set of an interface, beside elements of another type, with the
// spread first, last, in the middle and twice.
func TestIRSpread_AtAnInterfaceElement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Named
    fn name(value: self) -> String
end

struct P
    n: Int
end

struct Q
    s: String
end

impl Named for P
    fn name(p: P) -> String
        "p${p.n}"
    end
end

impl Named for Q
    fn name(q: Q) -> String
        "q${q.s}"
    end
end

fn names(xs: Iter<Named>) -> String
    xs |> Iter.map(fn x -> Named.name(x)) |> String.join(",")
end

fn show(xs: Iter<Display>) -> String
    xs |> Iter.map(fn x -> "${x}") |> String.join(",")
end

fn sorted(xs: Iter<Display>) -> String
    xs |> Iter.map(fn x -> "${x}") |> Iter.sort() |> String.join(",")
end

fn main()
    xs = [1, 2]
    a: List<Display> = [..xs, "a"]
    io.print(show(a))
    b: List<Display> = ["a", ..xs]
    io.print(show(b))
    c: List<Display> = [..xs, ..["a"]]
    io.print(show(c))
    d: List<Display> = ["a", ..xs, "b", ..xs]
    io.print(show(d))
    e: List<Display> = [..xs]
    io.print(show(e))
    vs = Vector[1, 2]
    f: Vector<Display> = [..xs, "a"]
    io.print(show(f))
    g: Vector<Display> = ["a", ..xs]
    io.print(show(g))
    h: Vector<Display> = ["a", ..xs, "b"]
    io.print(show(h))
    i: Vector<Display> = [..vs, 3]
    io.print(show(i))
    ss = Set[1, 2]
    j: Set<Display> = [..ss, 3]
    io.print(sorted(j))
    k: Set<Display> = [3, ..ss, ..xs]
    io.print(sorted(k))
    l: List<Display> = [..ss, "a"]
    io.print(sorted(l))
    m: List<Display> = ["a", ..Some(1)]
    io.print(show(m))
    n: List<Display> = [..1..3, "a"]
    io.print(show(n))
    ps = [P{n: 1}]
    o: List<Named> = [..ps, Q{s: "x"}]
    io.print(names(o))
    p: List<Named> = [Q{s: "y"}, ..ps]
    io.print(names(p))
    q: Vector<Named> = [..Vector[P{n: 2}], Q{s: "z"}]
    io.print(names(q))
    io.print(show([..xs, "lit"]))
    io.print(show(["lit", ..xs]))
    io.print(show(Vector[..xs, "lit"]))
    plain = [..xs, 3]
    io.print(show(plain))
    plain2 = [0, ..xs]
    io.print(show(plain2))
    r: List<Display> = [..[], "a"]
    io.print(show(r))
    ms = {1 => "a"}
    t: Map<Int, String> = {..ms, 2 => "b"}
    io.print(sorted(t))
end
`
	irRunSource(t, src, "1,2,a\n"+
		"a,1,2\n"+
		"1,2,a\n"+
		"a,1,2,b,1,2\n"+
		"1,2\n"+
		"1,2,a\n"+
		"a,1,2\n"+
		"a,1,2,b\n"+
		"1,2,3\n"+
		"1,2,3\n"+
		"1,2,3\n"+
		"1,2,a\n"+
		"a,1\n"+
		"1,2,a\n"+
		"p1,qx\n"+
		"qy,p1\n"+
		"p2,qz\n"+
		"1,2,lit\n"+
		"lit,1,2\n"+
		"1,2,lit\n"+
		"1,2,3\n"+
		"0,1,2\n"+
		"a\n"+
		"(1, a),(2, b)\n")
}

// A Vector or Set spread into a literal of its own collection at another
// element type is built at that type, as a List spread is: beside an
// element of another type, alone, and as an enum's embedded type whose
// elements a match then tells apart.
func TestIRSpread_ItsOwnCollectionAtAnotherElement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Circle
    r: Int
end

struct Dot
    at: Int
end

enum Shape
    embeds Circle
    embeds Dot
end

fn show(xs: Iter<Display>) -> String
    xs |> Iter.map(fn x -> "${x}") |> Iter.sort() |> String.join(",")
end

fn kinds(xs: Iter<Shape>) -> String
    xs
    |> Iter.map(fn s ->
        case s
            .Circle{r:} -> "circle ${r}"
            .Dot{at:} -> "dot ${at}"
        end
    end)
    |> Iter.sort()
    |> String.join(",")
end

fn main()
    xs = [1, 2]
    vs = Vector[1, 2]
    ss = Set[1, 2]
    a: Vector<Display> = [..vs, "a"]
    io.print(show(a))
    b: Vector<Display> = ["a", ..vs, ..xs]
    io.print(show(b))
    c: Set<Display> = [..ss, "a"]
    io.print(show(c))
    d: Vector<Display> = [..vs]
    io.print(show(d))
    e: Set<Display> = [..ss]
    io.print(show(e))
    f: List<Display> = [..xs]
    io.print(show(f))
    cs = Vector[Circle{r: 1}]
    cset = Set[Circle{r: 2}]
    g: Vector<Shape> = [..cs, Dot{at: 1}]
    io.print(kinds(g))
    h: Set<Shape> = [..cset, Dot{at: 2}]
    io.print(kinds(h))
    i: Vector<Shape> = [..cs]
    io.print(kinds(i))
    j: Set<Shape> = [..cset]
    io.print(kinds(j))
    nn: Vector<NonNegativeInt> = [1, 2]
    k: Vector<Int> = [..nn, -1]
    io.print(show(k))
end
`
	irRunSource(t, src, "1,2,a\n"+
		"1,1,2,2,a\n"+
		"1,2,a\n"+
		"1,2\n"+
		"1,2\n"+
		"1,2\n"+
		"circle 1,dot 1\n"+
		"circle 2,dot 2\n"+
		"circle 1\n"+
		"circle 2\n"+
		"-1,1,2\n")
}
