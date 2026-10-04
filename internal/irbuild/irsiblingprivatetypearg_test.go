package irbuild

import "testing"

// A file's private struct as the type argument of another file's public
// generic function, generic struct and generic enum runs on the VM
// (testdata/sibling_private_typearg).
//
// Each instance is built in span.nomi's gen and so mirrors main.nomi's private
// `Point` there. The program names `Point` only in its own file, which is
// legal; the analyzer rejects every spelling that would name a private type
// from another file. The `shown` call dispatches `Display` on the private type
// from inside the declaring file's instance, and `Debug.inspect` renders an
// instance whose fields are the private type.
func TestIRSiblingPrivateTypeArg_InstancesOverACallersPrivateTypeRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "ident 1\n" +
		"<p2>\n" +
		"span 3..4\n" +
		"Span{start: Point{x: 3}, stop: Point{x: 4}}\n" +
		"full 5\n" +
		"empty\n"
	got := vmReference(fixture("sibling_private_typearg/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
