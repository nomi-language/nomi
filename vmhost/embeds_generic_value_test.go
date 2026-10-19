package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A value, not a construction, of a type holding an embedded type, where the
// same type at the embedding enum is expected, is rebuilt at the wider type:
// `w: Maybe<Shape> = m` for `m: Maybe<Circle>`. A Shape holding a Circle is
// another record than the Circle, so the value is made again, not relabelled.
// Each shape runs: Maybe (Some and None), Result (Ok and Err), a tuple, a
// list, a set, a map (keys and values), a user generic enum's positional,
// struct-shaped and bare variants, a user generic struct, a nested
// List<Maybe<Circle>> and Maybe<Maybe<Circle>>, and an embedded distinct.
// Each position runs: a binding, a function argument, a return, and either
// side of `==` and `!=`.
func TestRun_EmbeddedValueInsideAGenericValueIsRebuilt(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

type Wrapped Circle

enum Shape {
  embeds Circle
  embeds Wrapped
  Dot
}

enum Box<T> {
  Full T
  Named{value: T, n: Int}
  Empty
}

struct Holder<T> {
  value: T
}

fn describe(s: Shape) -> String {
  case s {
    #Shape.Circle{r} -> "circle ${r}"
    Shape.Wrapped(#{r}) -> "wrapped ${r}"
    Shape.Dot -> "dot"
  }
}

fn take(m: Maybe<Shape>) -> String {
  case m {
    Some(s) -> "some ${describe(s)}"
    None -> "none"
  }
}

fn give(m: Maybe<Circle>) -> Maybe<Shape> {
  m
}

fn boxed(b: Box<Shape>) -> String {
  case b {
    Box.Full(s) -> "full ${describe(s)}"
    #Box.Named{value, n} -> "named ${describe(value)} ${n}"
    Box.Empty -> "empty"
  }
}

fn main() {
  c = #Circle{r: 1}
  s: Shape = c
  m = Some(c)
  none: Maybe<Circle> = None
  w: Maybe<Shape> = m
  io.inspect(w)
  io.inspect(Some(c) == w)
  io.print(take(m))
  io.print(take(none))
  io.inspect(give(m))
  io.inspect(m == Some(Shape.Dot))
  io.inspect(m == Some(s))
  io.inspect(Some(s) == m)
  io.inspect(Some(Shape.Dot) != m)
  io.inspect(m != Some(s))
  io.inspect(none == Some(Shape.Dot))
  ok: Result<Circle, Circle> = Ok(c)
  bad: Result<Circle, Circle> = Err(#Circle{r: 2})
  rs: Result<Shape, Shape> = ok
  rb: Result<Shape, Shape> = bad
  io.inspect(rs)
  io.inspect(rb)
  io.inspect(ok == Ok(Shape.Dot))
  io.inspect(ok == Ok(s))
  io.inspect(bad == Err(s))
  io.inspect(Err(#Circle{r: 2}) == rb)
  t = #(1, c)
  tw: #(Int, Shape) = t
  io.inspect(tw)
  io.inspect(t == #(1, Shape.Dot))
  io.inspect(#(1, s) == t)
  l = [c, #Circle{r: 3}]
  lw: List<Shape> = l
  io.print(lw |> Iter.map(describe) |> String.join(", "))
  io.inspect(l == [s, #Circle{r: 3}])
  io.inspect([s, Shape.Dot] == l)
  io.inspect(l != [s, Shape.Dot])
  st = %[c]
  sw: Set<Shape> = st
  io.inspect(Set.contains?(sw, s))
  mp = %{c => #Circle{r: 5}}
  mw: Map<Shape, Shape> = mp
  io.inspect(Map.get(mw, s))
  io.inspect(mp == %{s => Shape.Dot})
  io.inspect(%{s => #Circle{r: 5}} == mp)
  b = Box.Full(c)
  bw: Box<Shape> = b
  io.print(boxed(bw))
  nb = #Box.Named{value: c, n: 7}
  io.print(boxed(nb))
  eb: Box<Circle> = Box.Empty
  io.print(boxed(eb))
  io.inspect(b == Box.Full(Shape.Dot))
  io.inspect(b == Box.Full(s))
  io.inspect(Box.Full(s) != b)
  nested = [Some(c), None]
  nw: List<Maybe<Shape>> = nested
  io.print(nw |> Iter.map(take) |> String.join(", "))
  io.inspect(nested == [Some(s), None])
  io.inspect(nested == [Some(Shape.Dot), None])
  wrapped = Some(Wrapped(#Circle{r: 9}))
  ww: Maybe<Shape> = wrapped
  io.print(take(ww))
  io.inspect(wrapped == Some(Shape.Wrapped(#Circle{r: 9})))
  h = #Holder{value: c}
  hw: Holder<Shape> = h
  io.print(describe(hw.value))
  io.inspect(h == #Holder{value: Shape.Dot})
  mm: Maybe<Maybe<Circle>> = Some(m)
  mmw: Maybe<Maybe<Shape>> = mm
  io.inspect(mmw)
}
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := `Some(#Circle{r: 1})
True
some circle 1
none
Some(#Circle{r: 1})
False
True
True
True
False
False
Ok(#Circle{r: 1})
Err(#Circle{r: 2})
False
True
False
True
#(1, #Circle{r: 1})
False
True
circle 1, circle 3
True
False
True
True
Some(#Circle{r: 5})
False
True
full circle 1
named circle 1 7
empty
False
True
False
some circle 1, none
True
False
some wrapped 9
True
circle 1
False
Some(Some(#Circle{r: 1}))
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A generic enum holding its own instance is rebuilt by an adapter that calls
// itself for the tail, alone and inside a list; a Vector and an anonymous
// record are rebuilt too.
func TestRun_EmbeddedValueInsideARecursiveGenericValueIsRebuilt(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

enum Shape {
  embeds Circle
  Dot
}

enum Chain<T> {
  Link{head: T, tail: Chain<T>}
  End
}

fn describe(s: Shape) -> String {
  case s {
    #Shape.Circle{r} -> "circle ${r}"
    Shape.Dot -> "dot"
  }
}

fn walk(c: Chain<Shape>) -> String {
  case c {
    #Chain.Link{head, tail} -> "${describe(head)} -> ${walk(tail)}"
    Chain.End -> "end"
  }
}

fn main() {
  t = #Chain.Link{head: #Circle{r: 1}, tail: #Chain.Link{head: #Circle{r: 2}, tail: Chain.End}}
  w: Chain<Shape> = t
  io.print(walk(w))
  io.print(walk(t))
  ts = [t, Chain.End]
  ws: List<Chain<Shape>> = ts
  io.print(ws |> Iter.map(walk) |> String.join(" | "))
  io.inspect(t == #Chain.Link{head: Shape.Dot, tail: Chain.End})
  io.inspect(t == w)
  c = #Circle{r: 1}
  v = #[c]
  vw: Vector<Shape> = v
  io.inspect(vw)
  io.inspect(v == #[Shape.Dot])
  r = #{a: c, n: 1}
  rw: #{a: Shape, n: Int} = r
  io.inspect(rw)
  io.inspect(r == #{a: Shape.Dot, n: 1})
}
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := `circle 1 -> circle 2 -> end
circle 1 -> circle 2 -> end
circle 1 -> circle 2 -> end | end
False
True
#[#Circle{r: 1}]
False
#{a: #Circle{r: 1}, n: 1}
False
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A comparison operand that needs an expected type (a literal holding
// `Plain.Spot`) is typed at the wider of the two operands' types when the
// other operand's is the narrower by `embeds`, in either order: a list, a
// tuple, a Maybe and a map, equal and not.
func TestRun_ComparisonLiteralTakesTheWiderType(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

enum Plain {
  embeds Circle
  Spot
}

fn main() {
  c = #Circle{r: 1}
  s: Plain = c
  io.inspect([c, c] == [s, Plain.Spot])
  io.inspect([c, c] == [s, #Plain.Circle{r: 1}])
  io.inspect([s, Plain.Spot] != [c, c])
  cs = [c]
  io.inspect(cs == [Plain.Spot])
  io.inspect(#(c, Plain.Spot) == #(s, Plain.Spot))
  io.inspect(#(s, Plain.Spot) == #(#Circle{r: 2}, Plain.Spot))
  m = Some(c)
  io.inspect(m == Some(Plain.Spot))
  io.inspect(Some(Plain.Spot) != m)
  io.inspect(%{c => c} == %{s => Plain.Spot})
  io.inspect(%{s => #Plain.Circle{r: 1}} == %{c => c})
  io.inspect([Some(c)] == [Some(s), Some(Plain.Spot)])
}
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := `False
True
True
False
True
False
False
True
False
True
False
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// The same comparisons in a test body, which the builder lowers for the
// test runner: every case passes and none is BLOCKED.
func TestEmbeddedValueInsideAGenericValue_RunsInTests(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "embeds_test.nomi"
	p, err := vmhost.LoadSource(name, `struct Circle {
  r: Int
}

enum Plain {
  embeds Circle
  Spot
}

test "a Maybe value against a wider construction" {
  c = #Circle{r: 1}
  m = Some(c)
  assert Some(c) == Some(#Plain.Circle{r: 1})
  refute Some(c) == Some(Plain.Spot)
  refute m == Some(Plain.Spot)
  s: Plain = c
  assert m == Some(s)
}

test "a list value against a wider literal" {
  c = #Circle{r: 1}
  s: Plain = c
  cs = [c, c]
  assert cs == [s, #Plain.Circle{r: 1}]
  refute cs == [s, Plain.Spot]
  w: List<Plain> = cs
  assert w == [s, s]
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	got := buf.String()
	if strings.Contains(got, "BLOCKED") || !strings.Contains(got, "2 passed, 0 failed") {
		t.Fatalf("want two passing cases:\n%s", got)
	}
}
