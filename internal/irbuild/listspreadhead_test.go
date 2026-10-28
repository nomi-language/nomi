package irbuild

import "testing"

// A dot variant at the head of a list literal with a spread takes the
// expected element type, in a plain function and in a generic one, and
// the list runs.
func TestIRListSpreadDotVariantHead_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

enum Color {
    Red
    Blue
}

enum Crumb<T> {
    Left T
    Right T
}

fn push<T>(x: T, rest: List<Crumb<T>>) -> List<Crumb<T>> {
    [.Right(x), ..rest]
}

fn main() {
    rest: List<Color> = [.Blue]
    a: List<Color> = [.Red, ..rest]
    io.inspect(a)
    io.inspect(push(1, [.Left(2)]))
}
`
	irRunSource(t, src, "[Red, Blue]\n[Right(1), Left(2)]\n")
}
