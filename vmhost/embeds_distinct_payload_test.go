package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An `embeds` of a distinct over a value matches its payload pattern against
// the distinct's inner value, as the checker types it: `Shape.Wrapped(#{r})`
// is a struct pattern over Circle for `type Wrapped Circle`. Each position a
// pattern is tested in runs, matched and not: a tail `case`, a pattern `if`,
// an `else` that leaves and one that supplies the payload, a variant payload
// nested in another, and a `case` whose value is bound. So do a literal,
// tuple, list, map and distinct payload pattern over the other embedded
// distincts, and a fallback whose payload pattern destructures a distinct.
func TestRun_EmbeddedDistinctPayloadPatterns(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

type Wrapped Circle

type UserId String

type Pair #(Int, Int)

type Ids List<Int>

type Kvs Map<String, Int>

type Meters Int

type Twice Meters

type Again Wrapped

enum Shape {
  embeds Wrapped
  embeds UserId
  embeds Pair
  embeds Ids
  embeds Kvs
  embeds Twice
  embeds Again
  Dot
}

enum Holder {
  Hold(Shape)
  Empty
}

fn tail_case(s: Shape) -> String {
  case s {
    Shape.Wrapped(#{r: 1}) -> "case one"
    Shape.Wrapped(#{r}) -> "case ${r}"
    _ -> "case miss"
  }
}

fn tail_if(s: Shape) -> String {
  if Shape.Wrapped(#{r}) = s {
    "if ${r}"
  } else {
    "if miss"
  }
}

fn else_leaves(s: Shape) -> String {
  Shape.Wrapped(#{r}) = s else {
    return "else miss"
  }
  "else ${r}"
}

fn else_fallback(s: Shape) -> String {
  Shape.Wrapped(#{r}) = s else {
    #Circle{r: 0}
  }
  "fallback ${r}"
}

fn nested(h: Holder) -> String {
  case h {
    .Hold(Shape.Wrapped(#{r})) -> "nested ${r}"
    _ -> "nested miss"
  }
}

fn mid_case(s: Shape) -> String {
  n = case s {
    Shape.Wrapped(#{r}) -> r
    _ -> -1
  }
  "mid ${n}"
}

fn literal(s: Shape) -> String {
  case s {
    Shape.UserId("ann") -> "user ann"
    _ -> "user miss"
  }
}

fn tuple(s: Shape) -> String {
  case s {
    Shape.Pair(#(1, b)) -> "pair ${b}"
    _ -> "pair miss"
  }
}

fn list(s: Shape) -> String {
  case s {
    Shape.Ids([x, ..]) -> "ids ${x}"
    _ -> "ids miss"
  }
}

fn map(s: Shape) -> String {
  case s {
    Shape.Kvs(%{"k" => v}) -> "kvs ${v}"
    _ -> "kvs miss"
  }
}

fn distinct(s: Shape) -> String {
  case s {
    Shape.Twice(Meters(m)) -> "twice ${m}"
    _ -> "twice miss"
  }
}

fn again(s: Shape) -> String {
  case s {
    Shape.Again(Wrapped(#{r})) -> "again ${r}"
    _ -> "again miss"
  }
}

fn pair_fallback(s: Shape) -> String {
  Shape.Pair(#(a, b)) = s else {
    #(0, 0)
  }
  "pair fallback ${a} ${b}"
}

fn again_fallback(s: Shape) -> String {
  Shape.Again(Wrapped(#{r})) = s else {
    Wrapped(#Circle{r: 0})
  }
  "again fallback ${r}"
}

fn main() {
  w = Shape.Wrapped(#Circle{r: 2})
  io.print(tail_case(Shape.Wrapped(#Circle{r: 1})))
  io.print(tail_case(w))
  io.print(tail_case(Shape.Dot))
  io.print(tail_if(w))
  io.print(tail_if(Shape.Dot))
  io.print(else_leaves(w))
  io.print(else_leaves(Shape.Dot))
  io.print(else_fallback(w))
  io.print(else_fallback(Shape.Dot))
  io.print(nested(Holder.Hold(w)))
  io.print(nested(Holder.Hold(Shape.Dot)))
  io.print(nested(Holder.Empty))
  io.print(mid_case(w))
  io.print(mid_case(Shape.Dot))
  io.print(literal(Shape.UserId("ann")))
  io.print(literal(Shape.UserId("bob")))
  io.print(tuple(Shape.Pair(#(1, 9))))
  io.print(tuple(Shape.Pair(#(2, 9))))
  io.print(list(Shape.Ids([4, 5])))
  io.print(list(Shape.Ids([])))
  io.print(map(Shape.Kvs(%{"k" => 3})))
  io.print(map(Shape.Kvs(%{"j" => 3})))
  io.print(distinct(Shape.Twice(Meters(8))))
  io.print(distinct(Shape.Dot))
  io.print(again(Shape.Again(Wrapped(#Circle{r: 6}))))
  io.print(again(Shape.Dot))
  io.print(pair_fallback(Shape.Pair(#(3, 4))))
  io.print(pair_fallback(Shape.Dot))
  io.print(again_fallback(Shape.Again(Wrapped(#Circle{r: 7}))))
  io.print(again_fallback(Shape.Dot))
}
`)
	want := strings.Join([]string{
		"case one",
		"case 2",
		"case miss",
		"if 2",
		"if miss",
		"else 2",
		"else miss",
		"fallback 2",
		"fallback 0",
		"nested 2",
		"nested miss",
		"nested miss",
		"mid 2",
		"mid -1",
		"user ann",
		"user miss",
		"pair 9",
		"pair miss",
		"ids 4",
		"ids miss",
		"kvs 3",
		"kvs miss",
		"twice 8",
		"twice miss",
		"again 6",
		"again miss",
		"pair fallback 3 4",
		"pair fallback 0 0",
		"again fallback 7",
		"again fallback 0",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// In a test body, a struct pattern on an embedded distinct's payload runs in
// an `assert pattern = value` and in a `case` arm, matched and not.
func TestRun_EmbeddedDistinctPayloadPatternsInATestBody(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

struct Circle {
  r: Int
}

type Wrapped Circle

enum Shape {
  embeds Wrapped
  Dot
}

test "assert matched" {
  assert Shape.Wrapped(#{r}) = Shape.Wrapped(#Circle{r: 2})
  io.print("assert ${r}")
}

test "assert missed" {
  assert Shape.Wrapped(#{r: _}) = Shape.Dot
}

test "case" {
  w = Shape.Wrapped(#Circle{r: 7})
  a = case w {
    Shape.Wrapped(#{r}) -> "wrapped ${r}"
    _ -> "dot"
  }
  d = Shape.Dot
  b = case d {
    Shape.Wrapped(#{r}) -> "wrapped ${r}"
    _ -> "dot"
  }
  io.print("${a}, ${b}")
  assert a == "wrapped 7"
  assert b == "dot"
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the test: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := strings.Join([]string{
		"ok main_test.nomi :: assert matched",
		"  assert 2",
		"FAIL main_test.nomi :: assert missed",
		"  line 20: pattern did not match",
		"    assert Shape.Wrapped(#{r: _}) = Shape.Dot",
		"    actual: Dot",
		"ok main_test.nomi :: case",
		"  wrapped 7, dot",
		"test result: FAILED. 2 passed, 1 failed",
	}, "\n") + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("report:\n%s\nwant:\n%s", got, want)
	}
}

// A prelude wrapper holds an enum with `embeds` variants: `None`,
// `Some(Shape.Dot)` and `Ok(...)` pass to a Maybe<Shape> or Result<Shape,
// String> parameter, and a value of the embedded type widens inside the
// wrapper (`Some(Wrapped(c))`), as it does in a list or a tuple. A struct
// field, `List.head`, `Map.get`, a nested Maybe and `==` over Maybe<Shape>
// run too.
func TestRun_PreludeWrappersHoldAnEnumWithEmbeds(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

type Wrapped Circle

enum Shape {
  embeds Wrapped
  Dot
}

enum Plain {
  embeds Circle
  Spot
}

struct Holder {
  shape: Maybe<Shape>
}

fn maybe(m: Maybe<Shape>) -> String {
  case m {
    Some(Shape.Wrapped(#{r})) -> "some ${r}"
    Some(_) -> "some dot"
    None -> "none"
  }
}

fn result(m: Result<Shape, String>) -> String {
  case m {
    Ok(Shape.Wrapped(c)) -> "ok ${c.r}"
    Ok(_) -> "ok dot"
    Err(e) -> "err ${e}"
  }
}

fn plain(m: Maybe<Plain>) -> String {
  case m {
    Some(#Plain.Circle{r}) -> "plain ${r}"
    Some(_) -> "plain spot"
    None -> "plain none"
  }
}

fn list(xs: List<Shape>) -> String {
  case xs {
    [Shape.Wrapped(#{r}), ..] -> "list ${r}"
    _ -> "list miss"
  }
}

fn tuple(t: #(Shape, Int)) -> String {
  case t {
    #(Shape.Wrapped(#{r}), n) -> "tuple ${r + n}"
    #(_, n) -> "tuple ${n}"
  }
}

fn main() {
  io.print(maybe(None))
  io.print(maybe(Some(Shape.Dot)))
  io.print(maybe(Some(Wrapped(#Circle{r: 1}))))
  io.print(maybe(Some(Shape.Wrapped(#Circle{r: 2}))))
  io.print(result(Ok(Shape.Dot)))
  io.print(result(Err("x")))
  io.print(result(Ok(Wrapped(#Circle{r: 3}))))
  io.print(plain(None))
  io.print(plain(Some(Plain.Spot)))
  io.print(plain(Some(#Circle{r: 4})))
  io.print(list([Shape.Dot]))
  io.print(list([Wrapped(#Circle{r: 5}), Shape.Dot]))
  io.print(tuple(#(Shape.Dot, 1)))
  io.print(tuple(#(Wrapped(#Circle{r: 6}), 1)))
  h = #Holder{shape: Some(Shape.Dot)}
  io.print(maybe(h.shape))
  shapes: List<Shape> = [Wrapped(#Circle{r: 8}), Shape.Dot]
  io.print(maybe(List.head(shapes)))
  m: Map<String, Shape> = %{"a" => Shape.Dot}
  io.print(maybe(Map.get(m, "a")))
  io.print(maybe(Map.get(m, "b")))
  nested: Maybe<Maybe<Shape>> = Some(Some(Shape.Dot))
  io.inspect(nested)
  io.inspect(Some(Shape.Dot) == Some(Shape.Dot))
  one: Maybe<Shape> = Some(Wrapped(#Circle{r: 1}))
  io.inspect(one == Some(Shape.Dot))
  io.inspect(one == Some(Shape.Wrapped(#Circle{r: 1})))
}
`)
	want := strings.Join([]string{
		"none",
		"some dot",
		"some 1",
		"some 2",
		"ok dot",
		"err x",
		"ok 3",
		"plain none",
		"plain spot",
		"plain 4",
		"list miss",
		"list 5",
		"tuple 1",
		"tuple 7",
		"some dot",
		"some 8",
		"some dot",
		"none",
		"Some(Some(Dot))",
		"True",
		"False",
		"True",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}
