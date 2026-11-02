package irbuild

import (
	"testing"
)

// An enum with a variant whose payload is a map of function values is built
// and rendered like any other.
func TestIRDotVariant_FunctionMapPayloadRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
enum Shape {
  Circle Float
  Polygon { sides: Map<String, (Int) -> Int> }
}
fn make() -> Shape { .Circle(1.0) }
fn main() { io.inspect(make()) io.inspect(Shape.Polygon{sides: %{"n" => fn x: Int -> x}}) }
`, "Circle(1.0)\nPolygon{sides: %{\"n\" => <function>}}\n")
}
