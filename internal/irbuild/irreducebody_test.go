package irbuild

import (
	"testing"
)

func TestIRReduceBody_FilterMapReduce(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 xs = [1, 2, 3, 4, 5]
 answer = xs
 |> Iter.filter(fn n -> n > 2)
 |> Iter.map(fn n -> n * n)
 |> Iter.reduce(0, fn acc, n -> acc + n)
 io.print(answer)
}
`, "50\n")
}

func TestIRReduceBody_SeedScopeAndOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source() -> List<Int> { io.print("source"); [1, 2] }
fn seed(n: Int) -> Int { io.print("seed"); n }
fn main() {
 x = 10
 io.print(Iter.reduce(source(), seed(x), fn acc, x -> { io.print(x); acc + x }))
 io.print(Iter.reduce([4, 5, 6], 0, fn acc: Int, n: Int -> acc + n))
}
`, "source\nseed\n1\n2\n13\n15\n")
}

func TestIRReduceBody_NestedIndependentSeeds(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 io.print(Iter.reduce([1, 2], 100, fn outer, n -> {
  outer + Iter.reduce([3, 4], n, fn inner, x -> inner + x)
 }))
}
`, "117\n")
}

func TestIRReduceBody_EmptyAndRepeatedFilter(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty() -> List<Int> { [] }
fn main() {
 xs = Iter.filter([1, 2, 3], fn n -> { io.print(n); n > 1 })
 io.print("created")
 io.print(Iter.reduce(xs, 0, fn acc, n -> acc + n))
 io.inspect(Iter.to_list(xs))
 io.print(Iter.reduce(empty(), 7, fn acc, n -> acc + n))
}
`, "created\n1\n2\n3\n5\n1\n2\n3\n[2, 3]\n7\n")
}

func TestIRReduceBody_CallbackFault(t *testing.T) {
	verifyIterFault(t, `import std/io
fn main() {
 io.print(Iter.reduce([1, 0, 2], 10, fn acc, n -> {
  io.print(n)
  acc / n
 }))
}
`, "line 5: division by zero", "1\n0\n")
}

// A Map whose values are Maps is an iteration source like any other map: the
// VM iterates its `#(K, V)` pairs, and the reduction reads each key.
func TestIRReduceBody_NestedMapSourceRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() { io.print(Iter.reduce(%{1 => %{2 => 3}}, 10, fn acc, p -> acc + p.0)) }
`, "11\n")
}
