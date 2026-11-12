package irbuild

import "testing"

func TestIRListPattern_RecursiveSum(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn sum(xs: List<Int>) -> Int
  case xs
    [] -> 0
    [head, ..tail] -> head + sum(tail)
  end
end
fn main()
  io.print(sum([1, 2, 3]))
  io.print(sum([]))
end
`, "6\n0\n")
}

func TestIRListPattern_ExactLengthsScopesAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn source() -> List<Int>
    io.print("subject")
    [1, 2, 3]
end
fn describe(xs: List<Int>) -> String
  case xs
    [] -> "empty"
    [_] -> "single"
    [_a, _b] -> "pair"
    [_, _, .._rest] -> "long"
  end
end
fn main()
  io.print(describe([]))
  io.print(describe([1]))
  io.print(describe([1, 2]))
  io.print(describe(source()))
  head = 9
  case source()
    [head, ..tail] ->
        io.print(head)
        io.inspect(tail)
    [] -> io.print("missing")
  end
  io.print(head)
end
`, "empty\nsingle\npair\nsubject\nlong\nsubject\n1\n[2, 3]\n9\n")
}

func TestIRListPattern_NestedListsAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  xs = [[1, 2], [3]]
  empty: List<Int> = []
  read = fn -> case xs
    [head, ..] -> head
    [] -> empty
  end
  io.inspect(read())
end
`, "[1, 2]\n")
}

func TestIRListPattern_MultilineBindings(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  xs = [1, 2, 3]
  case xs
    [
      head,
      _,
      ..tail
    ] ->
      io.print(head)
      io.inspect(tail)
    _ -> io.print("short")
  end
end
`, "1\n[3]\n")
}
