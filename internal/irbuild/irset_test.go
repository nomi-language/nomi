package irbuild

import (
	"testing"
)

// A Set's size builds a struct whose field holds a Map of function values.
func TestIRSet_BesideAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Point
    x: Int
end
struct Outer
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = {}
end
fn main()
    s = Set[1, 2]
    io.inspect(Outer{p: [Point{x: Set.size(s)}]})
end
`, "Outer{p: [Point{x: 2}], hooks: {}}\n")
}

// A declared distinct is a set element: hashed and compared as `==` is.
func TestIRSet_DeclaredElementsRun(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
type Meter Int
fn main()
  s = Set[Meter(1), Meter(2), Meter(1)]
  io.print(Set.size(s))
  io.print(Set.contains(s, Meter(2)))
end
`, "2\nTrue\n")
}

func TestIRSet_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn add(s: Set<Int>, n: Int) -> Set<Int>
    Set.insert(s, n)
end
fn main()
  one = 1
  two = 2
  s = Set[one, two, 3, two, one]
  io.print(Set.size(s))
  io.print(Set.contains(s, 2))
  io.print(Set.contains(s, 9))
  more = add(s, 4)
  fewer = Set.remove(more, 2)
  io.inspect(s)
  io.inspect(more)
  io.inspect(fewer)
  io.inspect(Set.insert(fewer, 2))
  has = fn n: Int -> Set.contains(fewer, n)
  io.print(has(2))
  io.print(has(4))
end
`, "3\nTrue\nFalse\nSet[1, 2, 3]\nSet[1, 2, 3, 4]\nSet[1, 3, 4]\nSet[1, 3, 4, 2]\nFalse\nTrue\n")
}

func TestIRSet_EmptyAndScalarSemantics(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn empty() -> Set<Int>
    Set[]
end
fn main()
  s: Set<Int> = Set[]
  io.inspect(s)
  io.print(Set.size(empty()))
  io.print(Set.contains(s, 1))
  io.inspect(Set.insert(s, 1))
  nan = 0.0 / 0.0
  zero = 0.0
  fs = Set[nan, nan, -0.0, zero]
  io.print(Set.size(fs))
  io.print(Set.contains(fs, nan))
  io.print(Set.contains(fs, 0.0))
  a = "a"
  io.inspect(Set[a, "b", a])
  yes = True
  io.inspect(Set[yes, False, yes])
end
`, "Set[]\n0\nFalse\nSet[1]\n2\nTrue\nTrue\nSet[\"a\", \"b\"]\nSet[True, False]\n")
}

func TestIRSet_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn set(s: Set<Int>) -> Set<Int>
    io.print("set")
    s
end
fn main()
  s = Set[mark(1), mark(2), mark(1)]
  more = Set.insert(set(s), mark(3))
  io.print(Set.contains(set(more), mark(2)))
  io.inspect(s)
end
`, "1\n2\n1\nset\n3\nset\n2\nTrue\nSet[1, 2]\n")
}
