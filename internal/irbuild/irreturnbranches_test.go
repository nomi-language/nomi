package irbuild

import "testing"

func TestIRReturnBranches_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named scoped values", `import std::io
fn choose(flag: Bool, n: Int) -> Int
  if flag then
    value = n + 1
    io.print("left")
    return value
  else
    value = n + 2
    io.print("right")
    return value
  end
end
fn main()
  choose(True, 5) |> io.print()
  choose(False, 5) |> io.print()
end`, "left\n6\nright\n7\n"},
		{"named bare returns", `import std::io
fn choose(flag: Bool)
  if flag then
      io.print("left")
      return
  end
  io.print("right")
end
fn main()
    choose(True)
    choose(False)
end`, "left\nright\n"},
		{"captured lambda", `import std::io
fn use(choose: (Bool) -> Int)
  choose(True) |> io.print()
  choose(False) |> io.print()
end
fn main()
  base = 8
  use(fn flag: Bool ->
    if flag then return base else return base + 1
  end)
end`, "8\n9\n"},
		{"guard then short-circuit returning arms", `import std::io
fn check() -> Bool
    io.print("check")
    True
end
fn choose(early: Bool, left: Bool) -> Int
  if early then return 0
  if left and check() then return 1 else return 2
end
fn main()
  choose(True, True) |> io.print()
  choose(False, True) |> io.print()
  choose(False, False) |> io.print()
end`, "0\ncheck\n1\n2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
