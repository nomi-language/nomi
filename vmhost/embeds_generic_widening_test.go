package vmhost_test

import (
	"testing"
)

// A value of an embedded type passed where a generic type is expected at the
// enum that embeds it builds the expected instance: `b: Box<Shape> =
// Box.Full(c)` for `c: Circle` is a Box<Shape>, not a Box<Circle>. The
// checker records the construction at the instance the program holds, and
// the payload widens where it sits. Each position runs: a positional and a
// struct-shaped variant, a generic struct, a function argument, a return, a
// list element, a nested Maybe, an embedded distinct and a generic
// function's result.
func TestRun_EmbeddedValueInUserGenericVariant(t *testing.T) {
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
  Named{value: T}
  Empty
}

struct Holder<T> {
  value: T
}

fn describe(s: Shape): String {
  case s {
    Shape.Circle{r} -> "circle ${r}"
    Shape.Wrapped({r}) -> "wrapped ${r}"
    Shape.Dot -> "dot"
  }
}

fn take(b: Box<Shape>): String {
  case b {
    Box.Full(s) -> "full ${describe(s)}"
    Box.Named{value} -> "named ${describe(value)}"
    Box.Empty -> "empty"
  }
}

fn give(c: Circle): Box<Shape> {
  Box.Full(c)
}

fn single<T>(x: T): List<T> {
  [x]
}

fn main() {
  c = Circle{r: 1}
  b: Box<Shape> = Box.Full(c)
  io.print(take(b))
  n: Box<Shape> = Box.Named{value: Circle{r: 2}}
  io.print(take(n))
  h: Holder<Shape> = Holder{value: c}
  io.print(describe(h.value))
  io.print(take(Box.Full(Circle{r: 3})))
  io.print(take(give(Circle{r: 4})))
  l: List<Box<Shape>> = [Box.Full(c), Box.Full(Shape.Dot), Box.Empty]
  io.print(l |> Iter.map(take) |> String.join(", "))
  m: Box<Maybe<Shape>> = Box.Full(Some(c))
  case m {
    Box.Full(Some(s)) -> io.print("maybe ${describe(s)}")
    _ -> io.print("maybe miss")
  }
  w: Box<Shape> = Box.Full(Wrapped(Circle{r: 5}))
  io.print(take(w))
  ss: List<Shape> = single(c)
  io.print(ss |> Iter.map(describe) |> String.join(", "))
}
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := `full circle 1
named circle 2
circle 1
full circle 3
full circle 4
full circle 1, full dot, empty
maybe circle 1
full wrapped 5
circle 1
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// Two equality operands whose types differ only by an embedded value inside
// a generic type compare as the wider type: the narrower operand is built at
// the other's type, so `Some(circle) == Some(Shape.Dot)` compares two
// Maybe<Shape> values. Each wrapper runs on either side, equal and not, with
// `==` and `!=`: Maybe, Result's Ok and Err, a list, a tuple, a user generic
// enum's positional and struct-shaped variant, a generic struct, a nested
// wrapper, a list of wrappers and an embedded distinct.
func TestRun_EqualityWidensAnEmbeddedValueInsideAGenericType(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
  r: Int
}

type Wrapped Circle

enum Plain {
  embeds Circle
  embeds Wrapped
  Spot
}

enum Box<T> {
  Full T
  Named{value: T}
  Empty
}

struct Holder<T> {
  value: T
}

fn main() {
  c = Circle{r: 1}
  s: Plain = c
  io.inspect(Some(Circle{r: 1}) == Some(Plain.Spot))
  io.inspect(Some(c) == Some(s))
  io.inspect(Some(Circle{r: 2}) == Some(s))
  io.inspect(Some(s) != Some(c))
  io.inspect(Some(Plain.Spot) == Some(c))
  io.inspect(Ok(c) == Ok(Plain.Spot))
  io.inspect(Err(c) == Err(s))
  io.inspect([c] == [s])
  io.inspect([c] != [s])
  io.inspect((1, c) == (1, s))
  io.inspect((2, c) == (1, s))
  io.inspect(Box.Full(c) == Box.Full(s))
  io.inspect(Box.Full(c) != Box.Full(Plain.Spot))
  io.inspect(Box.Named{value: c} == Box.Named{value: s})
  io.inspect(Holder{value: c} == Holder{value: s})
  io.inspect(Holder{value: c} == Holder{value: Plain.Spot})
  io.inspect(Box.Full(Some(c)) == Box.Full(Some(s)))
  io.inspect([Some(c)] == [Some(s)])
  io.inspect(Some(Wrapped(c)) == Some(s))
  io.inspect(Some(Wrapped(c)) == Some(Plain.Wrapped(c)))
}
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := `False
True
False
False
False
False
True
True
False
True
False
True
True
True
True
False
True
True
False
True
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
