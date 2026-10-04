package irbuild

import "testing"

// A module-level typealias declared in another file runs on the VM in both
// spellings (testdata/typealias_imported).
//
// `shapes.Flat` and `shapes.Board` are named through the file's qualifier,
// which typeOf's QualifiedType arm resolved only against type declarations.
// `Board` imported by name has the target `Set<Cell>`, and `Cell` is bound in
// shapes.nomi's scope and not in main.nomi's, so expanding the target's
// spelling in the using file refused it. Both now take the target the checker
// resolved in the declaring file.
func TestTypeAlias_ImportedAndQualifiedAliasesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "2\n" +
		"2\n" +
		// 1*2 + 3*4
		"14\n" +
		"6\n"
	got := vmReference(fixture("typealias_imported/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
