package irbuild

import "testing"

// Functions named as values through an owner (testdata/owner_func_value): an
// inherent function of a local type and of another file's type, an interface
// function over Int, Float, a local type and another file's type,
// `Debug.inspect`, an annotated binding, a two-parameter interface function,
// and another file's plain function through
// a `self` import. Each is a function whose body is the qualified call the
// value stands for, so an interface function calls the impl of the element's
// type.
func TestOwnerFuncValue_RunsTheQualifiedCall(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "[x] milk\n" +
		"[ ] eggs\n" +
		"[4, 9]\n" +
		"[1, 2]\n" +
		"[1.5]\n" +
		"[milk, eggs]\n" +
		"[square 4]\n" +
		// Debug.inspect of "a" is `"a"` with its quotes, which the list's
		// Display shows as written.
		"[\"a\"]\n" +
		"5\n" +
		"[1, 2, 3]\n" +
		"Some(\"hi!\")\n"
	got := vmReference(fixture("owner_func_value/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
