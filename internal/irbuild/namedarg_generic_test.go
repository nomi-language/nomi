package irbuild

import (
	"strings"
	"testing"
)

// TestVM_NamedArgGeneric runs named arguments to a generic function, direct
// and piped and in any order. The checker used to check a generic call's named
// argument against the parameter at its written position, so
// `spread(1, twice: True)` was rejected.
func TestVM_NamedArgGeneric(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"[1, 1]",
		"[1, 1]",
		"[1, 1, 2]",
		"[1, 1, 2, 3]",
		"[4, 5]",
		`["a", "b"]`,
		"12",
		"",
	}, "\n")
	if got := vmReference(fixture("named_arg_generic.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
