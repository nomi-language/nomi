package irbuild

import (
	"strings"
	"testing"
)

// TestVM_ElselessIf runs `if`s with no `else`, which are statements: a
// branch that returns, an `else if` chain whose branches print and whose
// first returns, a pipe's `if` stage, an `if` in a lambda's body, and
// `if`s whose arms end in `assert`, `refute` and `dbg`.
func TestVM_ElselessIf(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		`Err("negative")`,
		"Ok(1)",
		`"negative"`,
		"zero",
		`"done"`,
		"small",
		`"done"`,
		`"done"`,
		"positive",
		"2",
		"3",
		"Ok(4)",
		"True",
		"dbg line 66: n = 1",
		"",
	}, "\n")
	if got := vmReference(fixture("elseless_if.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
