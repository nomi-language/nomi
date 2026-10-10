package irbuild

import (
	"strings"
	"testing"
)

// TestVM_PartialOfPartial runs partial applications of partial applications:
// positional and named `_`, the two mixed, a positional `_` written after a
// named argument, three levels deep with a default left open, a partial over
// a struct field and over a function parameter, a partial over a lambda
// binding, and a partial whose lambda argument routes to the trailing
// callback. The checker used to type `c(port: _, host: "x")` over a local
// partial `c` as a full call, so the next call was "`String` is not a
// function", and the builder placed a partial's positional arguments without
// skipping a slot a name had claimed.
func TestVM_PartialOfPartial(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"a:1/30",
		"positional:2/30",
		"named:3/30",
		"swapped:4/30",
		"swapped:8080/30",
		"mixed:6/5",
		"deep:7/30",
		"deep:8080/30",
		"lone:8/30",
		"field:9/30",
		"arg:7/30",
		"lambda/10",
		"lambda/5",
		"21",
		"",
	}, "\n")
	if got := vmReference(fixture("partial_of_partial.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
