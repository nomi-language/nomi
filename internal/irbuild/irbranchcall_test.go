package irbuild

import "testing"

func TestIRBranchCall_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"direct written order", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn combine(a: Int, b: Int) -> Int
    a * 10 + b
end
fn main()
  combine(mark(1), if True then mark(2) else mark(9)) |> io.print()
  combine(b: if False then mark(8) else mark(4), a: mark(3)) |> io.print()
end`, "1\n2\n12\n4\n3\n34\n"},
		{"indirect case argument", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
  offset = 10
  combine = fn a: Int, b: Int -> offset + a + b
  combine(case 1
      0 -> mark(8)
      _ -> mark(2)
  end, mark(3)) |> io.print()
end`, "2\n3\n15\n"},
		{"branch before default", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn combine(a: Int, b: Int = mark(5)) -> Int
    a + b
end
fn main()
  combine(if True then mark(2) else mark(8)) |> io.print()
end`, "2\n5\n7\n"},
		{"empty-list branch argument", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn choose(xs: List<Int>, n: Int) -> Int
    _ = xs
    n
end
fn main()
  choose(if True then [] else [mark(9)], mark(3)) |> io.print()
  choose(if False then [] else [mark(2)], mark(4)) |> io.print()
end`, "3\n3\n2\n4\n4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
