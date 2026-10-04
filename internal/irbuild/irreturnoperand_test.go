package irbuild

import "testing"

func TestIRReturnOperand_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"short circuit", `import std/io
fn mark(): Bool { io.print("marked") True }
fn choose(flag: Bool): Bool { return flag and mark() }
fn main() { choose(False) |> io.print() choose(True) |> io.print() }`, "False\nmarked\nTrue\n"},
		{"captured lambda", `import std/io
fn mark(): Bool { io.print("marked") True }
fn main() {
  base = False
  choose = |flag: Bool| { return base or (flag and mark()) }
  choose(True) |> io.print()
  choose(False) |> io.print()
}`, "marked\nTrue\nFalse\n"},
		{"guard before final operand", `import std/io
fn mark(): Bool { io.print("marked") True }
fn choose(early: Bool, flag: Bool): Bool {
  if early { return False }
  return flag or mark()
}
fn main() {
  choose(True, False) |> io.print()
  choose(False, True) |> io.print()
  choose(False, False) |> io.print()
}`, "False\nTrue\nmarked\nTrue\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
