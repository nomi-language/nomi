package irbuild

import "testing"

func TestIRListOps_EmptyTailAndPersistence(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  xs = [1, 2]
  empty: List<Int> = []
  io.inspect(List.head(xs))
  io.inspect(List.head(empty))
  io.inspect(List.tail(xs))
  io.inspect(List.tail([1]))
  io.inspect(List.tail(empty))
  io.inspect(List.concat(xs, [3]))
  io.inspect(List.concat([], xs))
  io.inspect(List.concat(xs, []))
  io.inspect(xs)
}
`, "Some(1)\nNone\nSome([2])\nSome([])\nNone\n[1, 2, 3]\n[1, 2]\n[1, 2]\n[1, 2]\n")
}

func TestIRListOps_NestedPayloadAndEvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(label: String, value: Int): List<Int> {
  io.print(label)
  [value]
}
fn first_tail(nested: List<List<Int>>): Maybe<List<Int>> {
  case List.head(nested) {
    Some(xs) -> List.tail(xs)
    None -> None
  }
}
fn main() {
  io.inspect(List.concat(source("left", 1), source("right", 2)))
  nested = [[1, 2], [3]]
  io.inspect(first_tail(nested))
  read = || List.head(nested)
  io.inspect(read())
}
`, "left\nright\n[1, 2]\nSome([2])\nSome([1, 2])\n")
}
