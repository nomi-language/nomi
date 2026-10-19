package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// Calling a value whose type is data, not a function, is an error at the
// callee. Before, the call was accepted and its result passed as any type.
func TestCallNonFunction_IsAnErrorAtTheCallee(t *testing.T) {
	errs := checkWithStdlib(`
struct P {
  a: Int
}

type Handler (Int) -> Int

fn main() {
  _ = 1()
  _ = ""()
  pair = #(1, 2)
  _ = pair(3)
  _ = [1](0)
  p = #P{a: 1}
  _ = p()
  f = fn x: Int -> x + 1
  _ = f(1)
  h = Handler(fn x -> x)
  _ = h(2)
  _ = Wrap.One(#[1])
}

enum Wrap {
  One Vector<Int>
  Zero
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"9:7 `Int` is not a function",
		"10:7 `String` is not a function",
		"12:7 `#(Int, Int)` is not a function",
		"13:7 `List<Int>` is not a function",
		"15:7 `P` is not a function",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A value of a type parameter, bounded or not, or of an interface type is
// not a function either, called directly, as a partial, through a field or
// as a pipe stage. The first is a lowering fuzz find: the checker admitted
// `x(rest)` with `x: T`, recorded no type for it, and the IR builder
// declined the body.
func TestCallNonFunction_TypeParamAndInterfaceValues(t *testing.T) {
	errs := checkWithStdlib(`
struct Box<T> {
  v: T
}

fn evens<T>(xs: List<T>) -> List<T> {
  case xs {
    [] -> []
    [x, ..rest] -> [x(rest)]
  }
}

fn bounded<T>(x: T) -> String where T: Display {
  x(1)
}

fn field<T>(b: Box<T>) -> Int {
  b.v(1)
}

fn piped<T>(x: Int, y: T) -> Int {
  x |> y()
}

fn partial<T>(x: T) -> Int {
  h = x(_)
  h(1)
}

fn iface(x: Display) -> String {
  x(1)
}

fn piped_int(x: Int, y: Int) -> Int {
  x |> y()
}

fn applies<T>(f: (T) -> T, x: T) -> T {
  f(x)
}

fn main() {
  _ = evens([1])
  _ = bounded(1)
  _ = field(#Box{v: 1})
  _ = piped(1, 2)
  _ = partial(1)
  _ = iface(1)
  _ = piped_int(1, 2)
  _ = applies(fn n -> n, 1)
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"9:21 `T` is not a function",
		"14:3 `T` is not a function",
		"18:3 `T` is not a function",
		"22:8 `T` is not a function",
		"26:7 `T` is not a function",
		"31:3 `Display` is not a function",
		"35:8 `Int` is not a function",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
