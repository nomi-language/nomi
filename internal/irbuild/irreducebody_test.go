package irbuild

import (
	"testing"
)

func TestIRReduceBody_FilterMapReduce(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 xs = [1, 2, 3, 4, 5]
 answer = xs
 |> Iter.filter(|n| n > 2)
 |> Iter.map(|n| n * n)
 |> Iter.reduce(|acc = 0, n| acc + n)
 io.print(answer)
}
`, "50\n")
}

func TestIRReduceBody_SeedScopeAndOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(): List<Int> { io.print("source"); [1, 2] }
fn seed(n: Int): Int { io.print("seed"); n }
fn main() {
 x = 10
 io.print(Iter.reduce(source(), |acc = seed(x), x| { io.print(x); acc + x }))
 io.print(Iter.reduce([4, 5, 6], |acc: Int, n: Int| acc + n))
}
`, "source\nseed\n1\n2\n13\n15\n")
}

func TestIRReduceBody_NestedIndependentSeeds(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 io.print(Iter.reduce([1, 2], |outer = 100, n| {
  outer + Iter.reduce([3, 4], |inner = n, x| inner + x)
 }))
}
`, "117\n")
}

func TestIRReduceBody_EmptyAndRepeatedFilter(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): List<Int> { [] }
fn main() {
 xs = Iter.filter([1, 2, 3], |n| { io.print(n); n > 1 })
 io.print("created")
 io.print(Iter.reduce(xs, |acc = 0, n| acc + n))
 io.inspect(Iter.to_list(xs))
 io.print(Iter.reduce(empty(), |acc = 7, n| acc + n))
}
`, "created\n1\n2\n3\n5\n1\n2\n3\n[2, 3]\n7\n")
}

func TestIRReduceBody_EmptyUnseededFault(t *testing.T) {
	verifyIterFault(t, `import std/io
fn empty(): List<Int> { [] }
fn main() { io.print(Iter.reduce(empty(), |acc: Int, n: Int| acc + n)) }
`, "Iter.reduce: cannot reduce empty collection without initial value", "")
}

func TestIRReduceBody_CallbackFault(t *testing.T) {
	verifyIterFault(t, `import std/io
fn main() {
 io.print(Iter.reduce([1, 0, 2], |acc = 10, n| {
  io.print(n)
  acc / n
 }))
}
`, "line 5: division by zero", "1\n0\n")
}

// A Map whose values are Maps is an iteration source like any other map: the
// VM iterates its `(K, V)` pairs, and the reduction reads each key.
func TestIRReduceBody_NestedMapSourceRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() { io.print(Iter.reduce({1 => {2 => 3}}, |acc = 10, p| acc + p.0)) }
`, "11\n")
}
