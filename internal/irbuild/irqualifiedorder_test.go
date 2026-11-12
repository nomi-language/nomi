package irbuild

import (
	"testing"
)

func TestIRQualifiedCall_SiblingArgumentEffectsStayInSourceOrder(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io lib
end
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn main()
  io.print(lib.sum(
    mark(1),
    mark(2) + 3
  ))
end
`, "1\n2\n6\n", map[string]string{
		"lib.nomi": "pub fn sum(a: Int, b: Int) -> Int\n    a + b\nend",
	})
}

func TestIRQualifiedCall_ArgumentEffectsStayInSourceOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Point
    x: Int
end
impl Point
  fn sum(p: Point, a: Int, b: Int) -> Int
      p.x + a + b
  end
end
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn main()
  p = Point{x: 0}
  result = Point.sum(p, mark(1), mark(2) + 3)
  io.print(result)
end
`, "1\n2\n6\n")
}
