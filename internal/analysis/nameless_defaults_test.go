package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// Defaults belong to a declaration, as parameter names do: a call through a
// function value with none behind it passes every argument. A partial bound
// to a name keeps its callee's defaults, and so does a copy of it.
func TestDefaultsThroughANamelessValue_AreAnError(t *testing.T) {
	errs := checkWithStdlib(`
fn connect(host: String, port: Int = 8080): String {
  host
}

fn id<T>(x: T): T {
  x
}

fn main() {
  c = connect(_, port: _)
  _ = c("a")
  r = id(c)
  _ = r("b")
  _ = r("b", 1)
  h = c
  _ = h("d")
  p = || connect(_, port: _)
  _ = p()("e")
  _ = case Some(c) {
    Some(k) -> k("f")
    None -> ""
  }
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s %v", e.Line, e.Col, e.Message, e.Hints))
	}
	hint := "[a function type's parameters have no defaults; pass every argument]"
	want := []string{
		"14:7 expected 2 arguments, got 1 " + hint,
		"19:7 expected 2 arguments, got 1 " + hint,
		"21:16 expected 2 arguments, got 1 " + hint,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The same rule holds where the callee is not a plain binding: a pipe
// through a nameless value, a callee that is not a name (a parenthesized
// name, an `if`, a `case`, a block), and a `once`, whose value is computed
// in its own initializer. Each passes with every argument, and a lambda
// literal called on the spot and a partial binding piped into keep their
// defaults.
func TestDefaultsThroughOtherNamelessCallees_AreAnError(t *testing.T) {
	errs := checkWithStdlib(`
fn connect(host: String, port: Int = 8080): String {
  host
}

fn id<T>(x: T): T {
  x
}

once oc = connect(_, port: _)

once ol = |s: String, n: Int = 5| s

fn main() {
  c = connect(_, port: _)
  r = id(c)
  _ = "a" |> r()
  _ = "a" |> r(_)
  _ = "a" |> oc()
  _ = (c)("b")
  b = True
  _ = (if b { c } else { r })("c")
  _ = case b {
    True -> c
    False -> r
  }("d")
  _ = { c }("e")
  _ = oc("f")
  _ = ol("g")
  _ = "a" |> r(1)
  _ = (if b { c } else { r })("c", 1)
  _ = oc("f", 1)
  _ = ol("g", 2)
  _ = "a" |> c()
  _ = (|x: Int, y: Int = 2| x + y)(1)
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s %v", e.Line, e.Col, e.Message, e.Hints))
	}
	hint := "[a function type's parameters have no defaults; pass every argument]"
	want := []string{
		"17:14 expected 2 arguments, got 1 (counting the piped value) " + hint,
		"18:14 expected 2 arguments, got 1 (counting the piped value) " + hint,
		"19:14 expected 2 arguments, got 1 (counting the piped value) " + hint,
		"20:7 expected 2 arguments, got 1 " + hint,
		"22:7 expected 2 arguments, got 1 " + hint,
		"23:7 expected 2 arguments, got 1 " + hint,
		"27:7 expected 2 arguments, got 1 " + hint,
		"28:7 expected 2 arguments, got 1 " + hint,
		"29:7 expected 2 arguments, got 1 " + hint,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
