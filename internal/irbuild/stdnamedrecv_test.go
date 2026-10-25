package irbuild

import (
	"strings"
	"testing"
)

// A program's impls of its own interfaces for std types no program file
// declares run (testdata/std_type_impls): `Date.to_v(d)` and
// `Date.from_v(v)`, qualified by the std type with an operand of it and with
// none; `Int.from_v(v)` from another file; `T.from_v(v)` in an instance at
// Date and at Maybe<Date>; and dispatch on a Date, a Decimal, an Instant and
// a Maybe held as the interface, the Decimal and the Instant boxed
// (irStdLeafBoxes) because their values name no type.
func TestStdNamedReceivers_ProgramImplsForStdTypesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Repeat("True\n", 8)
	got := vmReference(fixture("std_type_impls/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
