package irbuild

import (
	"testing"
)

func TestIRSet_DiscardedBuildPreservesNativeEmission(t *testing.T) {
	for _, src := range []string{
		`struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn main() { s = #{1, 2} dbg Outer{p: [Point{x: Set.size(s)}]} return }`,
	} {
		p, err := AnalyzeSource("main.nomi", src)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "main" {
					t.Fatal("unsupported nominal body retained")
				}
			}
		}
	}
}

// A declared distinct is a set element: hashed and compared as `==` is.
func TestIRSet_DeclaredElementsRun(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Meter Int
fn main() {
  s = #{Meter(1), Meter(2), Meter(1)}
  io.print(Set.size(s))
  io.print(Set.contains?(s, Meter(2)))
}
`, "2\nTrue\n")
}

func TestIRSet_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn add(s: Set<Int>, n: Int): Set<Int> { Set.insert(s, n) }
fn main() {
  s = #{1, 2, 3, 2, 1}
  io.print(Set.size(s))
  io.print(Set.contains?(s, 2))
  io.print(Set.contains?(s, 9))
  more = add(s, 4)
  fewer = Set.remove(more, 2)
  io.inspect(s)
  io.inspect(more)
  io.inspect(fewer)
  io.inspect(Set.insert(fewer, 2))
  has = |n: Int| Set.contains?(fewer, n)
  io.print(has(2))
  io.print(has(4))
}
`, "3\nTrue\nFalse\n#{1, 2, 3}\n#{1, 2, 3, 4}\n#{1, 3, 4}\n#{1, 3, 4, 2}\nFalse\nTrue\n")
}

func TestIRSet_EmptyAndScalarSemantics(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): Set<Int> { #{} }
fn main() {
  s: Set<Int> = #{}
  io.inspect(s)
  io.print(Set.size(empty()))
  io.print(Set.contains?(s, 1))
  io.inspect(Set.insert(s, 1))
  nan = 0.0 / 0.0
  fs = #{nan, nan, -0.0, 0.0}
  io.print(Set.size(fs))
  io.print(Set.contains?(fs, nan))
  io.print(Set.contains?(fs, 0.0))
  io.inspect(#{"a", "b", "a"})
  io.inspect(#{True, False, True})
}
`, "#{}\n0\nFalse\n#{1}\n2\nTrue\nTrue\n#{\"a\", \"b\"}\n#{True, False}\n")
}

func TestIRSet_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn set(s: Set<Int>): Set<Int> { io.print("set") s }
fn main() {
  s = #{mark(1), mark(2), mark(1)}
  more = Set.insert(set(s), mark(3))
  io.print(Set.contains?(set(more), mark(2)))
  io.inspect(s)
}
`, "1\n2\n1\nset\n3\nset\n2\nTrue\n#{1, 2}\n")
}
