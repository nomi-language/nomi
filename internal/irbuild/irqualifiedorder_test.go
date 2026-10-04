package irbuild

import (
	"testing"
)

func TestIRQualifiedCall_SiblingArgumentEffectsStayInSourceOrder(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io lib }
fn mark(n: Int): Int { io.print(n) return n }
fn main() {
  io.print(lib.sum(
    mark(1),
    mark(2) + 3
  ))
}
`, "1\n2\n6\n", map[string]string{
		"lib.nomi": "pub fn sum(a: Int, b: Int): Int { a + b }",
	})
}

func TestIRQualifiedCall_ArgumentEffectsStayInSourceOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Point { x: Int }
impl Point {
  fn sum(p: Point, a: Int, b: Int): Int { p.x + a + b }
}
fn mark(n: Int): Int { io.print(n) return n }
fn main() {
  p = Point{x: 0}
  result = Point.sum(p, mark(1), mark(2) + 3)
  io.print(result)
}
`, "1\n2\n6\n")
}
