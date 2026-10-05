package irbuild

import "testing"

func TestIRStructEquality_TourPoints(t *testing.T) {
	verifyLambdaProgram(t, `struct Point {
  x: Int
  y: Int
}

fn main(): Map<Point, String> {
  dbg Point{x: 1, y: 2} == Point{x: 1, y: 2}
  dbg Point{x: 1, y: 2} == Point{x: 1, y: 3}
  dbg {Point{x: 1, y: 2} => "home"}
}
`, "dbg line 7: Point{x: 1, y: 2} == Point{x: 1, y: 2} = True\ndbg line 8: Point{x: 1, y: 2} == Point{x: 1, y: 3} = False\ndbg line 9: {Point{x: 1, y: 2} => \"home\"} = {Point{x: 1, y: 2} => \"home\"}\n")
}

func TestIRStructEquality_FloatFieldsAndInequality(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Reading {
  label: String
  value: Float
}
fn same(a: Reading, b: Reading): Bool { a == b }
fn main() {
  nan = 0.0 / 0.0
  io.print(same(Reading{label: "a", value: nan}, Reading{label: "a", value: nan}))
  io.print(Reading{label: "a", value: 0.0} != Reading{label: "a", value: -0.0})
  io.print(Reading{label: "a", value: 1.0} != Reading{label: "b", value: 1.0})
  m = {Reading{label: "a", value: 1.0} => 1, Reading{label: "b", value: 2.0} => 2, Reading{label: "a", value: 1.0} => 3}
  _ = dbg m
}
`, "True\nFalse\nTrue\ndbg line 13: m = {Reading{label: \"a\", value: 1.0} => 3, Reading{label: \"b\", value: 2.0} => 2}\n")
}

// `==` on a struct with a hand-written Equatable calls the impl rather than
// comparing structurally; `!=` negates its answer.
func TestIRStructEquality_HandWrittenImplIsCalled(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Loose {
  id: Int
  note: String
}
impl Equatable for Loose {
  fn equal?(a: Loose, b: Loose): Bool { a.id == b.id }
}
fn same(a: Loose, b: Loose): Bool { a == b }
fn main() {
  io.print(same(Loose{id: 1, note: "x"}, Loose{id: 1, note: "y"}))
  io.print(Loose{id: 1, note: "x"} != Loose{id: 2, note: "x"})
}
`, "True\nTrue\n")
}
