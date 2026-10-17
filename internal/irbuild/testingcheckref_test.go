package irbuild

import "testing"

// `testing.check` named as a value, by its qualifier and through a
// selective import: an intrinsic with no body, so its value is a function
// whose body is the check of its parameter. It declined as a std function
// with nothing to name.
func TestTestingCheckRef_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import {
    std/io
    std/testing
    std/testing.{check as verify}
}

fn main() {
    c: (Bool) -> Result<Bool, testing.AssertionFailure> = testing.check
    io.print(Result.ok?(c(True)))
    io.print(Result.ok?(c(False)))
    rs = [Some(1), None] |> Iter.map(testing.check) |> Iter.map(Result.ok?) |> Iter.to_list()
    io.inspect(rs)
    v: (Bool) -> Result<Bool, testing.AssertionFailure> = verify
    io.print(Result.ok?(v(False)))
}
`, "True\nFalse\n[True, False]\nFalse\n")
}
