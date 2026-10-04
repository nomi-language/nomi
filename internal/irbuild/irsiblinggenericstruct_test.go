package irbuild

import "testing"

// A literal of a generic struct declared in another file runs on the VM under
// both spellings: the selectively imported `Span{...}` and the file-qualified
// `span.Span{...}` (testdata/sibling_generic_struct).
//
// The template table is per file, so both spellings used to miss it: the bare
// name was looked up only among this file's own templates, and the qualified
// one fell through to the variant reading. Both now resolve through
// genericTemplateNamed, by declaration, and instantiate in the owner's file.
func TestIRSiblingGenericStruct_LiteralsNameTheDeclaringFilesTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("sibling_generic_struct/main.nomi")
	want := "bare width = 4\n" +
		"qualified width = 7\n" +
		"widened = 13\n" +
		"label = a..z\n" +
		"label bare = m..n\n" +
		"equal = True\n"
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
