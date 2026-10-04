package irbuild

import (
	"testing"
)

func TestIRRange_DiscardedBuildPreservesNativeEmission(t *testing.T) {
	src := `struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn main() { r = 1..5 dbg Outer{p: [Point{x: if Range.contains?(r, 3) { 1 } else { 0 }}]} return }`
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
				t.Fatal("unsupported nominal Debug retained")
			}
		}
	}
}

func TestIRRange_NaNOrdering(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  nan = 0.0 / 0.0
  io.print(Range.contains?(nan..=nan, nan))
  io.print(Range.contains?(0.0..=nan, nan))
  io.print(Range.contains?(0.0..nan, nan))
  io.print(Range.contains?(nan..=1.0, 0.0))
  io.print(Range.contains?(-0.0..=0.0, 0.0))
}
`, "False\nFalse\nFalse\nFalse\nTrue\n")
}

func TestIRRange_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn mark_range(r: Range<Int>): Range<Int> { io.print("range") r }
fn main() {
  io.print(Range.contains?(mark_range(mark(1)..=mark(5)), mark(3)))
  r = mark(2)..mark(8)
  r |> Range.contains?(mark(8)) |> io.print()
}
`, "1\n5\nrange\n3\nTrue\n2\n8\n8\nFalse\n")
}

func TestIRRange_Containment(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn interval(): Range<Int> { 1..=5 }
fn main() {
  io.print(Range.contains?("a".."m", "h"))
  io.print(Range.contains?(0.0..=1.0, 1.0))
  io.print(Range.contains?(1..5, 5))
  io.print(Range.contains?(interval(), 5))
  io.print(Range.contains?(5..1, 3))
  io.print(Range.bounded?(interval()))
  r = interval()
  inside = |n: Int| Range.contains?(r, n)
  io.print(inside(0))
  io.print(inside(3))
}
`, "True\nTrue\nFalse\nTrue\nFalse\nTrue\nFalse\nTrue\n")
}

func TestIRRange_OrderingDependencies(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/comparable.Ordering.{Less as Low}
}
fn lower(): Ordering { Low }
fn rank(o: Ordering): Int {
  case o {
    Ordering.Less -> -1
    Ordering.Equal -> 0
    Ordering.Greater -> 1
  }
}
fn main() {
  io.print(rank(lower()))
  io.print(rank(Int.compare(2, 2)))
  io.print(rank(String.compare("z", "a")))
}
`, "-1\n0\n1\n")
}
