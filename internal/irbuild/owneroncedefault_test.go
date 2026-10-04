package irbuild

import "testing"

// An owner-level `once` read as a parameter default runs on the VM: a reduce
// seed, a `fn` default and a lambda default (testdata/owner_once_default.nomi).
//
// ownerOnceValue resolves `Count.zero` from the reference the checker records
// at the member's position. The checker never checked a default under an
// annotated parameter, so it recorded nothing there and the read declined as
// `a field access on the type name Count: .zero`.
func TestOwnerOnce_ReadAsAParameterDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "3\n" +
		"5\n" +
		"15\n" +
		"7\n"
	got := vmReference(fixture("owner_once_default.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
