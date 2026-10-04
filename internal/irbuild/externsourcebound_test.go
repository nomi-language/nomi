package irbuild

import (
	"path/filepath"
	"testing"
)

// TestSourceBoundExterns_TheDifferentialGuardIsDischarged checks that
// vmReference prepares the FFI wrapper for a `gopkg` project, as `nomi test`
// does.
//
// Without the preparation a `gopkg` project's reference run reports
// "N externs declared but not registered", and fifteen corpus files read as
// wrong.
//
// It asserts on vmReference, the harness function itself, rather than on a
// corpus outcome. A sweep assertion would also catch this, and it would catch
// it as fifteen DIFFs in a 20-minute test — which is the difference between a
// diagnosis and a symptom.
func TestSourceBoundExterns_TheDifferentialGuardIsDischarged(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "tests",
		"18-ffi-and-dynamic", "tagged_ffi_app"))
	if err != nil {
		t.Fatal(err)
	}
	// A `gopkg` project whose Go module IS resolvable, so the wrapper path is
	// the one under test. `main.nomi` prints, which is what makes the
	// observation non-vacuous: a file with no output would match trivially
	// whether or not the externs were registered.
	got := vmReference(filepath.Join(root, "main.nomi"))
	if got.stdout != "FIXTURE\n" || got.exit != 0 {
		t.Fatalf("the VM did not run the FFI project through its wrapper:\n%s\n\n"+
			"If stderr names unregistered externs, the FFI preparation has been removed from "+
			"`vmReference` and every `gopkg` file in the corpus will read as DIFFED — the "+
			"VM refusing to load a program the wrapper runs correctly.", got)
	}
}
