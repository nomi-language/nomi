package irbuild

import "testing"

// A value of an `embeds` type in a position typed as the enum dispatches as
// the enum. `b: Shape = Circle{...}` is a Shape (spec §8, Subtype coercion),
// so `Display.to_string(b)` runs Shape's impl: the VM types the value
// statically.
//
// Every impl prints its own name, so a line of output says which one ran.

const embedsDispatchDecls = `import std/io

struct Circle {
  radius: Float
}

enum Shape {
  embeds Circle
  Dot
}

impl Display for Circle {
  fn to_string(_c: Circle): String {
    "circle-display"
  }
}

impl Display for Shape {
  fn to_string(_s: Shape): String {
    "shape-display"
  }
}

impl Debug for Circle {
  fn inspect(_c: Circle): String {
    "circle-debug"
  }
}

impl Debug for Shape {
  fn inspect(_s: Shape): String {
    "shape-debug"
  }
}

impl Equatable for Circle {
  fn equal?(_a: Circle, _b: Circle): Bool {
    io.print("circle-eq")
    True
  }
}

impl Equatable for Shape {
  fn equal?(_a: Shape, _b: Shape): Bool {
    io.print("shape-eq")
    True
  }
}

impl Comparable for Circle {
  fn compare(_a: Circle, _b: Circle): Ordering {
    io.print("circle-cmp")
    Ordering.Equal
  }
}

impl Comparable for Shape {
  fn compare(_a: Shape, _b: Shape): Ordering {
    io.print("shape-cmp")
    Ordering.Less
  }
}

`

// The VM retains this program, and its output is checked against the literal:
// a binding, a declared result and a parameter, each dispatching through the
// enum.
func TestIREmbedsDispatch_StaticTypeDecides(t *testing.T) {
	verifyLambdaProgram(t, embedsDispatchDecls+`fn make(): Shape {
  Circle{radius: 2.0}
}

fn same(a: Shape, b: Shape): Bool {
  Equatable.equal?(a, b)
}

fn main() {
  bound: Shape = Circle{radius: 1.0}
  q = Shape.Circle{radius: 1.0}
  io.inspect(bound)
  io.inspect(make())
  Equatable.equal?(bound, q) |> io.print()
  same(Circle{radius: 3.0}, q) |> io.print()
  (bound < q) |> io.print()
}
`, "shape-debug\nshape-debug\nshape-eq\nTrue\nshape-eq\nTrue\nshape-cmp\nTrue\n")
}
