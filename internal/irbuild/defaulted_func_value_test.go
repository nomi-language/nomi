package irbuild

import (
	"strings"
	"testing"
)

// TestVM_DefaultedFuncValue runs a function with a defaulted parameter used
// where a one-parameter function is expected, through a lambda and through a
// partial application whose unmentioned slot takes the `False` default. A
// partial left a Bool default unlowered (irClosedLiteral admitted only
// numeric and string literals), so `Iter.map(words, parse(_))` was BLOCKED.
func TestVM_DefaultedFuncValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"[Some(1), Some(2), None]",
		"[Some(1), Some(2), None]",
		"[Some(1), None, None]",
		"Some(3)",
		"Some(4)",
		"",
	}, "\n")
	if got := vmReference(fixture("defaulted_func_value.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
