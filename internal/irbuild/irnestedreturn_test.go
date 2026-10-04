package irbuild

import "testing"

func TestIRNestedReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"guard operand", `import std/io
fn mark(): Bool { io.print("marked") True }
fn choose(early: Bool, flag: Bool): Bool {
  if early { return flag and mark() }
  io.print("continued")
  False
}
fn main() {
  choose(True, False) |> io.print()
  choose(True, True) |> io.print()
  choose(False, True) |> io.print()
}`, "False\nmarked\nTrue\ncontinued\nFalse\n"},
		{"both returning operands", `import std/io
fn mark(): Bool { io.print("marked") True }
fn choose(early: Bool, flag: Bool): Bool {
  if early { return flag and mark() } else { return flag or mark() }
}
fn main() {
  choose(True, False) |> io.print()
  choose(True, True) |> io.print()
  choose(False, True) |> io.print()
  choose(False, False) |> io.print()
}`, "False\nmarked\nTrue\nTrue\nmarked\nTrue\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
