package irbuild

import "testing"

func TestIRDistinctBinding_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named unwrap and operand effects", `import std/io
type Id Int
fn mark(n: Int): Id { io.print("make") Id(n) }
fn read(id: Id): Int {
  Id(value) = id
  value + 1
}
fn main() {
  Id(value) = mark(
    4
  )
  io.print(value)
  read(Id(8)) |> io.print()
}`, "make\n4\n9\n"},
		{"captured lambda and guard arm", `import std/io
type Label String
fn main() {
  label = Label("hello")
  read = |early: Bool| {
    if early {
      Label(text) = label
      return text
    }

    Label(text) = label
    text + "!"
  }
  read(True) |> io.print()
  read(False) |> io.print()
}`, "hello\nhello!\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
