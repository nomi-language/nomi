package irbuild

import (
	"strings"
	"testing"
)

// A `todo` arm fits beside any other arm of an inferred region, before or
// after it: a Unit arm, an untyped `[]` arm, and an `if` whose else is
// `todo`.
func TestIRTodoArmBesideAnyArm_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

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

// An annotated discard binding of a `todo`, with or without a reason, lowers
// to the placeholder's trap as an unannotated `_ = todo` does, before
// another statement and as the body's last one: the program prints what
// runs before it and stops there. An annotated discard of a value ending a
// body runs too.
func TestIRAnnotatedTodoBinding_Traps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	for _, tc := range []struct{ name, value, after, reason string }{
		{"reason", `todo "x"`, "    io.print(\"after\")\n", ": x\n"},
		{"bare", "todo", "    io.print(\"after\")\n", "\n"},
		{"reason last", `todo "x"`, "", ": x\n"},
		{"bare last", "todo", "", "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import std::io\n\nfn main() {\n    io.print(\"before\")\n    _n: Int = " + tc.value + "\n" + tc.after + "}\n"
			if _, err := AnalyzeSource("entry.nomi", src); err != nil {
				t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
			}
			got := vmReference(writeTemp(t, src))
			if got.stdout != "before\n" || got.exit == 0 || !strings.Contains(got.stderr, "todo reached at ") || !strings.HasSuffix(got.stderr, ":5"+tc.reason) {
				t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
			}
		})
	}
	irRunSource(t, "import std::io\n\nfn main() {\n    io.print(\"ran\")\n    _n: Int = 1\n}\n", "ran\n")
}
