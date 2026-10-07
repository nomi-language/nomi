package irbuild

import "testing"

func TestIRIterTerminal_CountAndShortCircuit(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): List<Int> { [] }
fn main() {
 io.print(Iter.count([]))
 io.print(Iter.count(empty()))
 io.print(Iter.count([1, 2, 3]))
 xs = Iter.filter([1, 2, 3, 4], |n| { io.print(n); n > 2 })
 io.print("created")
 io.print(Iter.empty?(xs))
 io.print(Iter.not_empty?(xs))
 io.print(Iter.count(xs))
 io.print(Iter.empty?(empty()))
 io.print(Iter.not_empty?(empty()))
 io.print(Iter.empty?([1]))
 io.print(Iter.not_empty?([1]))
}
`, "0\n0\n3\ncreated\n1\n2\n3\nFalse\n1\n2\n3\nTrue\n1\n2\n3\n4\n2\nTrue\nFalse\nFalse\nTrue\n")
}

func TestIRIterTerminal_CallbackFault(t *testing.T) {
	for _, op := range []string{"count", "empty?", "not_empty?"} {
		t.Run(op, func(t *testing.T) {
			verifyIterFault(t, `import std/io
fn main() {
 xs = Iter.map([0, 1], |n| {
  io.print(n)
  10 / n
 })
 io.print("created")
 io.print(Iter.`+op+`(xs))
}
`, "line 5: division by zero", "created\n0\n")
		})
	}
}

func TestIRIterTerminal_TourPipelines(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 io.print(Iter.count(Iter.filter([1, 2, 3, 4, 5], |n| n > 2)))
 [1, 2, 3, 4, 5]
 |> Iter.filter(|n| n > 2)
 |> Iter.count()
 |> io.print()
}
`, "3\n3\n")
}

func TestIRIterTerminal_EmptyCountStandalone(t *testing.T) {
	verifyLambdaProgram(t, "fn main() { _ = Iter.count([]) }\n", "")
}
