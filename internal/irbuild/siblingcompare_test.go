package irbuild

import "testing"

// Sibling types imported under aliases order the same way through every
// route that asks for `Comparable.compare` (testdata/siblingcompare_alias).
// The range rows are the ones siblingCompareAt answers; it used to decline an
// aliased receiver, so `Doodad{n: 1}..=Doodad{n: 7}` was "not supported yet".
// The index it reads is keyed on the type's declaration, so the alias the
// importing file binds cannot change which impl answers.
func TestSiblingCompare_AnAliasedTypeImportOrdersARange(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("siblingcompare_alias/main.nomi")
	got := vmReference(path)
	want := "True\nFalse\nTrue\nTrue\nLess\nGreater\n[0, 1, 2]\n[1, 2]\n" +
		"True\nGreater\nTrue\n2\nTrue\nTrue\n[z, b]\n" +
		"True\nFalse\n[1, 4, 7]\nFalse\n[2, 4]\n"
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if golden.stdout != got.stdout || golden.exit != got.exit {
		t.Errorf("golden record differs from the VM (exit %d):\n%s", golden.exit, golden.stdout)
	}
}
