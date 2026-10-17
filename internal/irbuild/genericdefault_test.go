package irbuild

import "testing"

// A generic function's omitted default is lowered at the call's instance:
// a lambda whose parameter takes its type from T, and an empty list of T.
func TestGenericDefault_LowersAtTheInstance(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn apply<T>(x: T, f: (T) -> T = fn y -> y) -> T {
    f(x)
}

fn wrap<T>(x: T, empty: List<T> = []) -> List<T> {
    [x, ..empty]
}

fn main() {
    io.print(apply(3))
    io.print(apply("s"))
    io.print(apply(3, fn y -> y + 1))
    io.inspect(wrap(1))
}
`, "3\ns\n4\n[1]\n")
}
