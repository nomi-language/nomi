package irbuild

import (
	"strings"
	"testing"
)

// TestVM_ElselessIf runs `if`s with no `else` whose branches have a value,
// which are Maybes: a single branch, an `else if` chain whose first branch
// returns, a branch that returns keeping the `if` a statement, a branch
// that is already a Maybe and a nested `if` (no flattening), a branch whose
// dot-variant takes the expected Maybe's payload, a pattern `if`, a generic
// body, a pipe's `if` stage, a binding, an annotation, a lambda, an
// argument, and an `Html` literal's content and optional-attribute slots.
func TestVM_ElselessIf(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		`Some("positive")`,
		"None",
		"None",
		`Some("zero")`,
		`Some("small")`,
		"None",
		`Err("negative")`,
		"Ok(1)",
		"Some(Some(1))",
		"Some(None)",
		"None",
		"Some(Some(1))",
		"Some(None)",
		"None",
		"Some(Circle(1.0))",
		"None",
		"Some(2)",
		"None",
		`Some("x")`,
		"None",
		"Some(8)",
		"None",
		"None",
		"Some(2)",
		"[None, Some(2), Some(3)]",
		"0",
		"<p>4</p>",
		"<p></p>",
		`<p class="on">x</p>`,
		"",
	}, "\n")
	if got := vmReference(fixture("elseless_if.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
