package irbuild

import "testing"

// A payload-free variant of a user generic enum runs where no expected type
// names its instance: a list or tuple element, an `if` branch, an operand of
// `==`, for a local enum and for another file's enum named bare and through
// its qualifier (testdata/generic_variant_bare).
//
// `Box.Empty` names no type argument, so without an expected type the builder
// had nothing to instantiate the template at and declined. The checker now
// records the instance it solved at each site (Symbol.VariantType), and
// genericVariant reads it. `tree.Tree.Leaf` had no reference at all, and the
// checker typed it as the uninstantiated declaration; it now resolves through
// the variant like `Tree.Leaf`.
func TestIRGenericVariantBare_BuildsAtTheCheckersInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("generic_variant_bare/main.nomi")
	want := "boxes = 3\n" +
		"equal lists = True\n" +
		"empty == full = False\n" +
		"picked = 0, 1\n" +
		"empty\n" +
		"pair full = True\n" +
		"none\n" +
		"qualified none\n" +
		"trees = -x-\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if golden.stdout != got.stdout || golden.exit != got.exit {
		t.Errorf("golden record differs from the VM (exit %d):\n%s", golden.exit, golden.stdout)
	}
}
