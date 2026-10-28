package irbuild

import (
	"strings"
	"testing"
)

// An owner function another file declares runs on the VM: a function of
// `impl Box<T>` or `impl Shape<T>` whose signature names none of the block's
// parameters (ownerimpl.go), called type-qualified through an aliased import,
// file-qualified (`boxes.Box.of`, `boxes.Shape.twice`), as a function value,
// with a defaulted parameter left out and given by name, generic over its own
// parameter (`of<U>`) and over a redeclared `<T>` (`first<T>`), from a program
// and from a test file (testdata/sibling_owner_fn). A function that names the
// block's T (`count`) takes the instance route, as before.
func TestSiblingOwnerFn_CallsReachTheDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "seven 7\n" +
		"size 2\n" +
		"#Box{items: [\"a\"]}\n" +
		"Some(4)\n" +
		"p  |\n" +
		"p |\n" +
		"value 7\n" +
		"[1, 1]\n" +
		"count 3\n" +
		"unit 1\n" +
		"#(\"x\", \"x\")\n" +
		"qualified 7 1\n" +
		"#(3, 3)\n"
	got := vmReference(fixture("sibling_owner_fn/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	tests := vmReference(fixture("sibling_owner_fn/calls_test.nomi"))
	if tests.exit != 0 || !strings.Contains(tests.stdout, "1 passed, 0 failed") {
		t.Fatalf("VM test run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", tests.exit, tests.stdout, tests.stderr)
	}
}
