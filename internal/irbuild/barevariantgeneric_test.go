package irbuild

import "testing"

// A bare `None` passed to a user generic beside the value that fixes its
// type takes that type, not the enclosing function's or lambda's return
// type: in a lambda whose expected result is a type parameter (`Maybe.map`'s
// U), and in a function returning `Maybe<String>`.
func TestBareVariantGeneric_TakesTheTypeItsCallFixes(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn f(): Maybe<String> {
    x = pick(False, None, Some(1))
    io.inspect(x)
    None
}

fn main() {
    io.inspect(Maybe.map(Some(94), |x| pick(False, None, Some(x))))
    io.inspect(Maybe.map(Some(2), |x| pick(True, Some(x), None)))
    io.inspect(Maybe.map(Some(3), |x| pick(True, Err("bad"), Ok(x))))
    io.inspect(f())
}
`, "Some(Some(94))\nSome(Some(2))\nSome(Err(\"bad\"))\nSome(1)\nNone\n")
}
