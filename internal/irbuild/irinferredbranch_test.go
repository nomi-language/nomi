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

// An `if` or `case` whose arms are a `List<Int>` and a generic call the
// expected `Iter<Int>` instantiated (`pick`'s T is the sequence there) is
// typed as the sequence, whichever arm comes first, and the list arm is
// viewed as it.
func TestIRInferredBranch_ListArmBesideASequenceArm(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn by_if(b: Bool, ys: List<Int>): List<Int> {
    Iter.filter(if b { ys } else { pick(True, [3], [4]) }, |x| x > 1)
    |> Iter.to_list()
}

fn by_case(m: Maybe<List<Int>>): List<Int> {
    Iter.filter(case m {
        Some(o) -> o
        None -> pick(False, [7], [8])
    }, |x| x > 5)
    |> Iter.to_list()
}

fn main() {
    io.inspect(by_if(True, [1, 2]))
    io.inspect(by_if(False, [1, 2]))
    io.inspect(by_case(Some([5, 6])))
    io.inspect(by_case(None))
}
`
	irRunSource(t, src, "[2]\n[3]\n[6]\n[8]\n")
}
