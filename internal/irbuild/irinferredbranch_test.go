package irbuild

import "testing"

func TestIRInferredBranch_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"output operand", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
  io.print(if True { mark(4) } else { mark(9) })
  io.print(case 2 { 0 -> "zero" _ -> "other" })
}`, "4\n4\nother\n"},
		{"lambda return", `import std/io
fn main() {
  base = 7
  choose = |flag: Bool| { return if flag { base } else { base + 1 } }
  choose(True) |> io.print()
  choose(False) |> io.print()
}`, "7\n8\n"},
		{"nested arithmetic", `import std/io
fn main() {
  io.print((if True { 4 } else { 8 }) + (case 1 { 0 -> 2 _ -> 3 }))
  io.print(if (if False { False } else { True }) { 1 } else { 2 })
}`, "7\n1\n"},
		{"nested value arms", `import std/io
fn choose(a: Bool, b: Bool): Int {
  if a { if b { 1 } else { 2 } } else { case 1 { 0 -> 3 _ -> 4 } }
}
fn main() {
  choose(True, True) |> io.print()
  choose(True, False) |> io.print()
  choose(False, True) |> io.print()
}`, "1\n2\n4\n"},
		{"conditional case subject", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
  io.print(case (if True { mark(2) } else { mark(9) }) {
    1 -> "one"
    _ -> "other"
  })
}`, "2\nother\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
