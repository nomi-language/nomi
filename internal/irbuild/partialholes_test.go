package irbuild

import "testing"

// A partial application over a generic function whose instantiation the
// checker left with an open hole: `pick(False, _, Some(Ok(6.5)))` fixes no
// error type for the Result. The partial reads its parameters off the
// checker's signature, which refused any hole, so it declined; a hole is
// filled as a call's operand fills it. Found by the rotating generated set.
func TestPartialOverAnOpenHole_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

fn pick<T>(c: Bool, a: T, b: T) -> T {
    if c { a } else { b }
}

fn main() {
    f = &pick(False, &1, Some(Ok(6.5)))
    io.inspect(f(None))
    g: (Maybe<Result<Float, String>>) -> Maybe<Result<Float, String>> = &pick(True, &1, Some(Ok(6.5)))
    io.inspect(g(Some(Err("e"))))
}
`, "Some(Ok(6.5))\nSome(Err(\"e\"))\n")
}
