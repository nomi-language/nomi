package irbuild

import "testing"

func TestIRFieldDefault_TourUserDefaults(t *testing.T) {
	verifyLambdaProgram(t, `struct User {
  name: String
  age: Int = 0
}

fn main() {
  alice = User{name: "Alice", age: 30}
  dbg alice.name
  dbg alice.age

  // The default lets the caller omit `+"`age`"+`.
  bob = User{name: "Bob"}
  dbg bob.age

  // Field-name punning: `+"`User{name, age}`"+` is shorthand for
  // `+"`User{name: name, age: age}`"+` when bindings of those names are in scope.
  name = "Carol"
  age = 28
  carol = User{name, age}
  dbg carol
}
`, "dbg line 8: alice.name = \"Alice\"\ndbg line 9: alice.age = 30\ndbg line 13: bob.age = 0\n"+
		"dbg line 20: carol = User{name: \"Carol\", age: 28}\n")
}

// Defaults run per construction, after the written fields, in declaration
// order, and resolve against the declaring file rather than the caller.
func TestIRFieldDefault_OrderAndScope(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(label: String, n: Int): Int {
  io.print(label)
  n
}
fn base(): Int { 7 }
struct Cfg {
  a: Int = mark("a", 1)
  b: Int
  c: Int = mark("c", base() * 2)
}
fn main() {
  base = 100
  x = Cfg{b: mark("b", base)}
  io.inspect(x.a + x.b + x.c)
  y = Cfg{c: 5, b: 1}
  io.inspect(y.a + y.c)
}
`, "b\na\nc\n115\na\n6\n")
}

func TestIRFieldDefault_DerivedStructDebug(t *testing.T) {
	verifyLambdaProgram(t, `struct Point {
  x: Int
  label: String = "origin \"0\""
  ok: Bool = True
}
fn main() {
  dbg Point{x: 3}
  dbg Point{x: -1, label: "a\nb", ok: False}
}
`, "dbg line 7: Point{x: 3} = Point{x: 3, label: \"origin \\\"0\\\"\", ok: True}\n"+
		"dbg line 8: Point{x: -1, label: \"a\\nb\", ok: False} = Point{x: -1, label: \"a\nb\", ok: False}\n")
}

func TestIRNominalDebug_TourDbgKinds(t *testing.T) {
	verifyLambdaProgram(t, `struct Point {
  x: Int
  y: Int
}

enum Status {
  Active
  Pending Int
}

type Email String

fn main() {
  dbg Point{x: 3, y: 4}
  dbg Status.Pending(7)
  dbg Email("a@b.com")
}
`, "dbg line 14: Point{x: 3, y: 4} = Point{x: 3, y: 4}\n"+
		"dbg line 15: Status.Pending(7) = Pending(7)\n"+
		"dbg line 16: Email(\"a@b.com\") = Email(\"a@b.com\")\n")
}

func TestIRNominalDisplay_TourPrintUser(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct User {
  name: String
  age: Int
}

impl Display for User {
  fn to_string(u: User): String {
    "${u.name} (${u.age})"
  }
}

fn main() {
  io.print(User{name: "Alice", age: 30})
}
`, "Alice (30)\n")
}

func TestIRNominalDisplay_InspectNamed(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct P {
  x: Int
  s: String
}
enum E {
  A
  B Int
}
fn make(n: Int): P {
  io.print("made")
  P{x: n, s: "q\"q"}
}
fn main() {
  io.inspect(make(2))
  io.inspect(E.B(3))
  io.inspect(E.A)
}
`, "made\nP{x: 2, s: \"q\\\"q\"}\nB(3)\nA\n")
}
