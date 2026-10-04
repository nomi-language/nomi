package irbuild

import "testing"

func TestIRStructVariant_TourShapeDebug(t *testing.T) {
	verifyLambdaProgram(t, `enum Direction {
  North
  South
  East
  West
}

enum Shape {
  Circle Float
  Rectangle {width: Float, height: Float}
}

fn main(): Shape {
  dbg Direction.North

  c = Shape.Circle(3.0)
  dbg c

  r = Shape.Rectangle{width: 4.0, height: 5.0}
  dbg r
}
`, "dbg line 14: Direction.North = North\ndbg line 17: c = Circle(3.0)\n"+
		"dbg line 20: r = Rectangle{width: 4.0, height: 5.0}\n")
}

func TestIRStructVariant_TourDotArea(t *testing.T) {
	verifyLambdaProgram(t, `enum Shape {
  Circle Float
  Rectangle {width: Float, height: Float}
}

fn area(s: Shape): Float {
  case s {
    .Circle(r) -> r * r * 3.14159
    .Rectangle{width, height} -> width * height
  }
}

fn main(): Float {
  dbg area(.Circle(3.0))
  dbg area(.Rectangle{width: 4.0, height: 5.0})
}
`, "dbg line 14: area(.Circle(3.0)) = 28.27431\n"+
		"dbg line 15: area(.Rectangle{width: 4.0, height: 5.0}) = 20.0\n")
}

// Written fields evaluate in source order and are forced; omitted fields take
// their defaults afterwards; patterns bind in their own order, may rename and
// may omit fields, and an unmatched variant falls through to a wildcard.
func TestIRStructVariant_OrderDefaultsAndBindings(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(label: String, n: Int): Int {
  io.print(label)
  n
}
enum Event {
  Idle
  Move {dx: Int, dy: Int = mark("dy", 7), tag: String = "m"}
  Say {text: String}
}
fn describe(e: Event): String {
  case e {
    Event.Move{tag, dx: horizontal} -> "${tag}:${horizontal}"
    .Say{text} -> text
    _ -> "idle"
  }
}
fn total(e: Event): Int {
  case e {
    .Move{dy, dx} -> dx + dy
    _ -> 0
  }
}
fn main() {
  a = Event.Move{tag: "t", dx: mark("dx", 2)}
  io.print(describe(a))
  io.print(total(a))
  io.print(describe(.Say{text: "hi"}))
  io.print(describe(Event.Idle))
  io.inspect(Event.Move{dx: 1, dy: 2})
}
`, "dx\ndy\nt:2\n9\nhi\nidle\nMove{dx: 1, dy: 2, tag: \"m\"}\n")
}
