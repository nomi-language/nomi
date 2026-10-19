package vmhost_test

import "testing"

// `None` in a lambda passed to a generic takes fresh inference variables
// for its type parameters, which the other branch or the other argument
// then solves. The checker rejected `if c { None } else { Some(2.5) }` there
// as a branch mismatch (`Maybe<T>` against `Maybe<Float>`), and recorded
// `pick(False, None, Some(x))` with `T` unsolved, so the builder declined it.
// With None in the first branch, the builder takes None's type from what
// the checker recorded for it rather than from the branch alone.
func TestRun_NoneInALambdaPassedToAGeneric(t *testing.T) {
	out := runOutput(t, `import std/io

fn apply<T, U>(x: T, f: (T) -> U) -> U {
  f(x)
}

fn pick<T>(c: Bool, a: T, b: T) -> T {
  if c { a } else { b }
}

fn main() {
  io.inspect(apply(1, fn x -> if x > 5 { Some(2.5) } else { None }))
  io.inspect(apply(9, fn x -> if x > 5 { Some(2.5) } else { None }))
  io.inspect(Maybe.map(Some(94), fn x -> pick(False, None, Some(x))))
  io.inspect(Maybe.map(Some(94), fn x -> pick(True, None, Some(x))))
  io.inspect(apply("a", fn s -> pick(False, Err("bad"), Ok(s))))
  io.inspect(Maybe.map(Some(1), fn x -> if x > 5 { None } else { Some(2.5) }))
  io.inspect(Maybe.map(Some(9), fn x -> if x > 5 { None } else { Some(2.5) }))
}
`)
	want := "None\nSome(2.5)\nSome(Some(94))\nSome(None)\nOk(\"a\")\nSome(Some(2.5))\nSome(None)\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A lambda argument to a generic whose parameter types only the call's
// expected type fixes takes them from it: `ident(fn x -> x + 1)` against
// `(Int) -> Int`. It was rejected as `expected (Int) -> Int, got (?) -> Unit`.
func TestRun_LambdaTypedByTheCallsExpectedType(t *testing.T) {
	out := runOutput(t, `import std/io

fn ident<T>(x: T) -> T {
  x
}

fn main() {
  f: (Int) -> Int = ident(fn x -> x + 1)
  io.inspect(f(1))
  g: (String) -> Int = ident(fn s -> String.length(s))
  io.inspect(g("abc"))
}
`)
	want := "2\n3\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A partial application of a generic, where a function type is expected,
// meets that type as the function of its open slots. The callee's result
// met it instead: `f: (Int) -> Int = pick(True, _, 4)` solved T as
// `(Int) -> Int` and rejected the 4.
func TestRun_PartialApplicationAgainstAFunctionType(t *testing.T) {
	out := runOutput(t, `import std/io

fn pick<T>(c: Bool, a: T, b: T) -> T {
  if c { a } else { b }
}

fn apply<T, U>(x: T, f: (T) -> U) -> U {
  f(x)
}

fn main() {
  f: (Int) -> Int = &pick(True, &1, 4)
  io.inspect(f(1))
  g: (String) -> String = &pick(False, &1, "b")
  io.inspect(g("a"))
  io.inspect(apply(3, &pick(False, &1, 4)))
}
`)
	want := "1\n\"b\"\n4\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A type declared in a block is known to every annotation in that block:
// a binding's, a lambda parameter's and its default's, a function type's,
// a turbofish's, and those in a nested block. The checker rejected each
// with `unknown type "Meters"`.
func TestRun_BlockLocalTypeInAnnotations(t *testing.T) {
	out := runOutput(t, `import std/io

fn ident<T>(x: T) -> T {
  x
}

fn main() {
  type Meters Int
  struct Pt {
    x: Int
  }
  m: Meters = Meters(3)
  f = fn x: Int, d: Meters = Meters(1) -> case d { Meters(n) -> x + n }
  g: (Meters) -> Int = fn Meters(n) -> n
  p: Pt = #Pt{x: 4}
  io.inspect(m)
  io.inspect(f(1))
  io.inspect(g(Meters(7)))
  io.inspect(ident<Meters>(Meters(2)))
  io.inspect(p)
  io.inspect({
    n: Meters = Meters(9)
    n
  })
}
`)
	want := "Meters(3)\n2\n7\nMeters(2)\n#Pt{x: 4}\nMeters(9)\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
