package irbuild

import (
	"strings"
	"testing"
)

// TestVM_TupleFunctionPart runs a tuple and an anonymous record holding a
// function value: built, projected, destructured, matched, passed, returned
// and called, with Debug rendering the function part as `<function>`.
func TestVM_TupleFunctionPart(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"destructured = 2",
		"projected = 11",
		"returned and passed = 49",
		"matched = 2",
		"captured = 101",
		"list = [4, 9]",
		"record = 6",
		"record destructured = 6",
		"debug = (1, <function>)",
		"record debug = {f: <function>, n: 3}",
		"nested debug = (Pt{x: 1}, <function>)",
		"",
	}, "\n")
	if got := vmReference(fixture("tuple_function_part.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}

func TestIRTuple_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"construction and indexing", `import std/io
fn make(n: Int): (Int, String) { (n + 1, "answer") }
fn main() { pair = make(41) io.print(pair.0) io.print(pair.1) }
`, "42\nanswer\n"},
		{"effects and destructuring", `import std/io
fn item(n: Int): Int { io.print(n) n + 10 }
fn main() {
  (a, _, c) = (item(
    1
  ), item(2), item(3))
  io.print(a) io.print(c)
}`, "1\n2\n3\n11\n13\n"},
		{"nested and destructured parameters", `import std/io
fn sum((a, b): (Int, Int)): Int { a + b }
fn main() {
  nested = ((10, 20), "nested")
  inner = nested.0
  io.print(sum(inner))
  io.print(nested.1)
}`, "30\nnested\n"},
		{"conditional and callable tuples", `import std/io
fn choose(flag: Bool): (Int, String) {
  if flag { (1, "first") } else { (2, "second") }
}
fn main() {
  get = |flag: Bool| choose(flag)
  (n, text) = get(False)
  io.print(n) io.print(text)
}`, "2\nsecond\n"},
		{"list and struct components", `import std/io
struct Point { x: Int }
fn main() {
  pair = ([1, 2], Point{x: 7})
  xs = pair.0
  point = pair.1
  io.inspect(xs) io.print(point.x)
}`, "[1, 2]\n7\n"},
		{"computed projection and discard", `import std/io
fn make(n: Int): (Int, Int) {
  io.print(n)
  (n, n + 1)
}
fn main() {
  io.print(make(10).1)
  (_, _) = make(20)
  Unit
}`, "10\n11\n20\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
