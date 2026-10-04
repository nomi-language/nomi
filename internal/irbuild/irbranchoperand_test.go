package irbuild

import "testing"

func TestIRBranchOperand_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"conditional return", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn choose(flag: Bool): Int { return if flag { mark(4) } else { mark(9) } }
fn main() { choose(True) |> io.print() choose(False) |> io.print() }`, "4\n4\n9\n9\n"},
		{"case return", `import std/io
fn choose(n: Int): String {
  return case n { 0 -> "zero" 1 -> "one" _ -> "other" }
}
fn main() {
  choose(0) |> io.print()
  choose(1) |> io.print()
  choose(2) |> io.print()
}`, "zero\none\nother\n"},
		{"guarded conditional return", `import std/io
fn choose(early: Bool, flag: Bool): Int {
  if early { return if flag { 4 } else { 9 } }
  io.print("continued")
  12
}
fn main() {
  choose(True, True) |> io.print()
  choose(True, False) |> io.print()
  choose(False, False) |> io.print()
}`, "4\n9\ncontinued\n12\n"},
		{"conditional default operand", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn choose(n: Int = if True { mark(4) } else { mark(9) }): Int { n + 1 }
fn main() {
  choose() |> io.print()
  choose(2) |> io.print()
}`, "4\n5\n3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
