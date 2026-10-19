package irbuild

import "testing"

// A `fn` declared in a struct's or a record variant's field default runs
// (testdata/field_default_nested_fn.nomi). The checker left its body and its
// call untyped, and the builder declined the default.
func TestFieldDefaultNestedFn_RunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("field_default_nested_fn.nomi")
	const want = "Unit\n" +
		"8\n" +
		"one\n" +
		"#Run{label: \"job\"}\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if got.stdout != golden.stdout || got.exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			got.exit, got.stdout, golden.exit, golden.stdout)
	}
}
