package irbuild

import "testing"

func TestIRListPattern_RecursiveSum(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn sum(xs: List<Int>): Int {
  case xs {
    [] -> 0
    [head, ..tail] -> head + sum(tail)
  }
}
fn main() {
  io.print(sum([1, 2, 3]))
  io.print(sum([]))
}
`, "6\n0\n")
}

func TestIRListPattern_ExactLengthsScopesAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(): List<Int> { io.print("subject") [1, 2, 3] }
fn describe(xs: List<Int>): String {
  case xs {
    [] -> "empty"
    [_] -> "single"
    [_a, _b] -> "pair"
    [_, _, .._rest] -> "long"
  }
}
fn main() {
  io.print(describe([]))
  io.print(describe([1]))
  io.print(describe([1, 2]))
  io.print(describe(source()))
  head = 9
  case source() {
    [head, ..tail] -> { io.print(head) io.inspect(tail) }
    [] -> io.print("missing")
  }
  io.print(head)
}
`, "empty\nsingle\npair\nsubject\nlong\nsubject\n1\n[2, 3]\n9\n")
}

func TestIRListPattern_NestedListsAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  xs = [[1, 2], [3]]
  empty: List<Int> = []
  read = || case xs {
    [head, .._] -> head
    [] -> empty
  }
  io.inspect(read())
}
`, "[1, 2]\n")
}

func TestIRListPattern_MultilineBindings(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  xs = [1, 2, 3]
  case xs {
    [
      head,
      _,
      ..tail
    ] -> {
      io.print(head)
      io.inspect(tail)
    }
    _ -> io.print("short")
  }
}
`, "1\n[3]\n")
}
