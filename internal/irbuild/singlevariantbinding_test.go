package irbuild

import "testing"

// `E.V(x) = value` over a single-variant enum binds V's payload
// (testdata/single_variant_binding.nomi): a positional payload, a struct-shaped
// variant's record, a generic payload, an embedded struct and an embedded
// wrapping distinct's inner value.
func TestSingleVariantBinding_RunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("single_variant_binding.nomi")
	const want = "3\n" +
		"#{a: 1, b: \"x\"}\n" +
		"held\n" +
		"#Circle{r: 2}\n" +
		"ada\n"
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
