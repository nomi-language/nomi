package irbuild

import "testing"

func TestIRTupleDebug_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"ordered components and transparent result", `import std/io
fn main() {
  pair = ("Ada", 37)
  (name, score) = pair
  dbg pair
  io.inspect((name, score))
}`, "dbg line 5: pair = (\"Ada\", 37)\n(\"Ada\", 37)\n"},
		{"nested tuple and list", `import std/io
fn main() {
  io.inspect(((1, True), ["a", "b"], (2.5, "c")))
}`, "((1, True), [\"a\", \"b\"], (2.5, \"c\"))\n"},
		{"effectful operand once", `import std/io
fn pair(): (Int, String) { io.print("pair") return (42, "x") }
fn main() {
  io.inspect(pair())
}`, "pair\n(42, \"x\")\n"},
		{"debug escaping", `import std/io
fn main() {
  io.inspect(("a\nb", "a\"b", "a\\b"))
}`, "(\"a\nb\", \"a\\\"b\", \"a\\\\b\")\n"},
		{"a distinct element", `import std/io
type Email String
fn main() {
  io.print("start")
  pair = (1, Email("a@b.com"))
  dbg pair
  io.inspect(pair)
}`, "start\ndbg line 6: pair = (1, Email(\"a@b.com\"))\n(1, Email(\"a@b.com\"))\n"},
		{"distinct and marker elements", `import std/io
type Meters Int
type Feet Int
impl Debug for Feet {
  fn inspect(f: Feet): String {
    "${Int(f)}ft"
  }
}
opaque type Secret String
type Coord (Int, Int)
type Unknown
fn main() {
  io.inspect((Meters(1), 2))
  io.inspect((Feet(3), [Meters(2)]))
  io.inspect((Secret("x"), 1))
  io.inspect((Coord((1, 2)), Unknown))
  io.inspect({a: Meters(4), b: Feet(5)})
  dbg (Meters(1), Feet(2))
  return
}`, "(Meters(1), 2)\n(3ft, [Meters(2)])\n(<opaque Secret>, 1)\n(Coord(1, 2), Unknown)\n{a: Meters(4), b: 5ft}\ndbg line 18: (Meters(1), Feet(2)) = (Meters(1), 2ft)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
