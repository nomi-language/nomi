package irbuild

import (
	"strings"
	"testing"
)

// TestPipe_ArgumentOrderIsPinned is the mutation guard for the one property a
// value-only fixture cannot see.
//
// `x |> f(a)` is `f(x, a)`. A builder that appended instead of prepending
// produces `f(a, x)`, which for a commutative callee is the same answer and for
// a non-commutative one is a different one — so `sub` and `between` are the two
// lines that fail under that mutation, and this test names them so the next
// reader knows which lines are load-bearing rather than illustrative.
//
// Appending makes `sub = 7` read `sub = -7` and `between = 5 <= 1 <= 9` read
// `between = 1 <= 9 <= 5`. Evaluating the stage's argument before the piped
// operand fails on `eval left` / `eval right`.
func TestPipe_ArgumentOrderIsPinned(t *testing.T) {
	got := vmReference(fixture("pipes.nomi"))
	for _, line := range []string{"sub = 7", "between = 5 <= 1 <= 9"} {
		if !strings.Contains(got.stdout, line) {
			t.Fatalf("the argument-order guard %q is gone from the fixture; a pipe that\n"+
				"appended instead of prepending would now pass:\n%s", line, got.stdout)
		}
	}
	// The order lines have to be adjacent and in this sequence: `left` is the
	// piped operand and `right` is the stage's written argument.
	if !strings.Contains(got.stdout, "eval left\neval right\n") {
		t.Fatalf("the evaluation-order guard is gone from the fixture:\n%s", got.stdout)
	}
}
