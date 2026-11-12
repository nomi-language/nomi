package irbuild

import "testing"

func TestIRTailReturn_UnannotatedLambda(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  base = 8
  read = fn early: Bool ->
    if early then return base
    value = base + 1
    return value
  end
  read(True) |> io.print()
  read(False) |> io.print()
end`, "8\n9\n")
}

func TestIRTailReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named valued return", `import std::io
fn mark(n: Int) -> Int
    io.print("returning")
    n
end
fn next(n: Int) -> Int
  value = n + 1
  return mark(
    value
  )
end
fn main()
  next(4) |> io.print()
end`, "returning\n5\n"},
		{"named bare return after dbg", `fn done()
  dbg "done"
  return
end
fn main()
    done()
end`, "dbg line 2: \"done\" = \"done\"\n"},
		{"lambda bare return after dbg", `fn main()
  done = fn ->
      dbg "done"
      return
  end
  done()
end`, "dbg line 3: \"done\" = \"done\"\n"},
		{"lambda valued return and guard", `import std::io
fn use(read: (Bool) -> Int)
  read(True) |> io.print()
  read(False) |> io.print()
end
fn main()
  base = 8
  use(fn early: Bool ->
    if early then return base
    value = base + 1
    return value
  end)
end`, "8\n9\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
