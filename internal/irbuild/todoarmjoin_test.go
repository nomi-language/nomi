package irbuild

import "testing"

// A `todo` arm fits beside any other arm of an inferred region, before or
// after it: a Unit arm, an untyped `[]` arm, and an `if` whose else is
// `todo`.
func TestIRTodoArmBesideAnyArm_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    io.inspect(case [Unit] {
        [p] -> p
        _ -> todo
    })
    io.inspect(case [[]] {
        [p] -> p
        _ -> todo
    })
    io.inspect(if 1 == 1 {
        Unit
    } else {
        todo
    })
    io.inspect(case [1] {
        [] -> todo
        _ -> Unit
    })
}
`
	irRunSource(t, src, "Unit\n[]\nUnit\nUnit\n")
}
