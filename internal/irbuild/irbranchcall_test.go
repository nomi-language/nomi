package irbuild

import "testing"

func TestIRBranchCall_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"direct written order", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn combine(a: Int, b: Int): Int { a * 10 + b }
fn main() {
  combine(mark(1), if True { mark(2) } else { mark(9) }) |> io.print()
  combine(b: if False { mark(8) } else { mark(4) }, a: mark(3)) |> io.print()
}`, "1\n2\n12\n4\n3\n34\n"},
		{"indirect case argument", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
  offset = 10
  combine = |a: Int, b: Int| offset + a + b
  combine(case 1 { 0 -> mark(8) _ -> mark(2) }, mark(3)) |> io.print()
}`, "2\n3\n15\n"},
		{"branch before default", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn combine(a: Int, b: Int = mark(5)): Int { a + b }
fn main() {
  combine(if True { mark(2) } else { mark(8) }) |> io.print()
}`, "2\n5\n7\n"},
		{"empty-list branch argument", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn choose(xs: List<Int>, n: Int): Int { _ = xs n }
fn main() {
  choose(if True { [] } else { [mark(9)] }, mark(3)) |> io.print()
  choose(if False { [] } else { [mark(2)] }, mark(4)) |> io.print()
}`, "3\n3\n2\n4\n4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
