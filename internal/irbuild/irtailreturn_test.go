package irbuild

import "testing"

func TestIRTailReturn_UnannotatedLambda(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  base = 8
  read = |early: Bool| {
    if early { return base }
    value = base + 1
    return value
  }
  read(True) |> io.print()
  read(False) |> io.print()
}`, "8\n9\n")
}

func TestIRTailReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named valued return", `import std/io
fn mark(n: Int): Int { io.print("returning") n }
fn next(n: Int): Int {
  value = n + 1
  return mark(
    value
  )
}
fn main() {
  next(4) |> io.print()
}`, "returning\n5\n"},
		{"named bare return", `import std/io
fn done() {
  io.print("done")
  return
}
fn main() { done() }`, "done\n"},
		{"lambda valued return and guard", `import std/io
fn use(read: (Bool) -> Int) {
  read(True) |> io.print()
  read(False) |> io.print()
}
fn main() {
  base = 8
  use(|early: Bool| {
    if early { return base }
    value = base + 1
    return value
  })
}`, "8\n9\n"},
		{"lambda bare return", `import std/io
fn main() {
  done = || { io.print("done") return }
  done()
}`, "done\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
