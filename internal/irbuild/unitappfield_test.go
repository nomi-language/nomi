package irbuild

import "testing"

// An application field of type Unit is published by boot, read, and
// written by `with` for the rest of a block, as any other field is.
func TestUnitAppField_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

struct App {
    v: Unit
    n: Int
}

fn boot() -> App {
    App{v: Unit, n: 1}
}

fn show() -> String {
    "${Debug.inspect(App.v)} ${App.n}"
}

fn main() {
    io.print(show())
    {
        with App.v = Unit
        with App.n = 2
        io.print(show())
    }
    io.print(show())
    f = fn -> App.v
    io.inspect(f())
}
`, "Unit 1\nUnit 2\nUnit 1\nUnit\n")
}
