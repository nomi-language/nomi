package irbuild

import "testing"

func TestIRNestedReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"guard operand", `import std::io
fn mark() -> Bool
    io.print("marked")
    True
end
fn choose(early: Bool, flag: Bool) -> Bool
  if early then return flag and mark()
  io.print("continued")
  False
end
fn main()
  choose(True, False) |> io.print()
  choose(True, True) |> io.print()
  choose(False, True) |> io.print()
end`, "False\nmarked\nTrue\ncontinued\nFalse\n"},
		{"both returning operands", `import std::io
fn mark() -> Bool
    io.print("marked")
    True
end
fn choose(early: Bool, flag: Bool) -> Bool
  if early then return flag and mark() else return flag or mark()
end
fn main()
  choose(True, False) |> io.print()
  choose(True, True) |> io.print()
  choose(False, True) |> io.print()
  choose(False, False) |> io.print()
end`, "False\nmarked\nTrue\nTrue\nmarked\nTrue\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
