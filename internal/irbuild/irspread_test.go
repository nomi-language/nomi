package irbuild

import "testing"

func TestIRSpread_TourStructUpdates(t *testing.T) {
	verifyLambdaProgram(t, `struct User {
  name: String
  age: Int
}

fn main() {
  alice = User{name: "Alice", age: 30}
  older = {..alice, age: 31}
  dbg older

  point = {x: 10, y: 20}
  dbg {..point, x: 9}

  // Punning works after a spread: `+"`age`"+` means `+"`age: age`"+`.
  age = 32
  dbg {..alice, age}
}
`, "dbg line 9: older = User{name: \"Alice\", age: 31}\n"+
		"dbg line 12: {..point, x: 9} = {x: 9, y: 20}\n"+
		"dbg line 16: {..alice, age} = User{name: \"Alice\", age: 32}\n")
}

// The base evaluates first, then each value in source order; the original is
// untouched, and a later field written twice through updates keeps its own
// value.
func TestIRSpread_OrderAndPersistence(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct P {
  x: Int
  y: Int
  label: String
}
fn mark(label: String, n: Int): Int {
  io.print(label)
  n
}
fn base(): P {
  io.print("base")
  P{x: 1, y: 2, label: "p"}
}
fn main() {
  q = {..base(), y: mark("y", 20), x: mark("x", 10)}
  io.print(q.x + q.y)
  r = {..q, label: "r"}
  io.print(r.label)
  io.print(q.label)
}
`, "base\ny\nx\n30\nr\np\n")
}
