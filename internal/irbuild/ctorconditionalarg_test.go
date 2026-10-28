package irbuild

import "testing"

// A generic constructor's argument that is an `if` or a `case` takes the
// payload type from the expected result in every branch, and runs.
func TestIRConstructorConditionalArgument_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

enum Color {
    Red
    Blue
}

struct Score {
    color: Color
    n: Int
}

fn pick(n: Int) -> Maybe<Color> {
    Some(if n == 1 { .Red } else { .Blue })
}

fn score(n: Int) -> Result<Score, String> {
    Ok(case n {
        1 -> #{color: .Red, n}
        _ -> #{color: .Blue, n}
    })
}

fn main() {
    io.inspect(pick(1))
    io.inspect(pick(2))
    io.inspect(score(2))
}
`
	irRunSource(t, src, "Some(Red)\nSome(Blue)\nOk(#Score{color: Blue, n: 2})\n")
}
