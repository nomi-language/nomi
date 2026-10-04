package irbuild

import "testing"

// A call to a generic function another file declares runs on the VM under
// both spellings, file-qualified and selectively imported (aliased too),
// at several type arguments (testdata/sibling_generic_fn).
//
// The instance is built in the declaring file's gen: a generic that calls
// another generic in its own file, one that takes a lambda, one with a
// `where` bound over a type from the calling file, one that calls a third
// file's generic, and two files in an import cycle whose generics call each
// other's (ping.nomi, pong.nomi). Files are lowered in name order after main, so
// zulu's call reaches zeta's `boxed` after zeta's walk, and that instance's
// call reaches alpha's `wrap` after alpha's walk and retry flush;
// irFlushLateInstances builds it.
func TestIRSiblingGenericFn_CallsInstantiateInTheDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "4\n" +
		"bare\n" +
		"point 1,2\n" +
		"tag kit\n" +
		"pair [7, 7]\n" +
		"apply 21\n" +
		"x!\n" +
		"<5>\n" +
		"<(3, 4)>\n" +
		"boxed [b, b, b]\n" +
		"boxed [2, 2, 2]\n" +
		"wrapped [True, True, True]\n" +
		"floats [1.5, 1.5, 1.5]\n" +
		"ping [p, p, p]\n" +
		"ping [(0, 1), (0, 1)]\n"
	got := vmReference(fixture("sibling_generic_fn/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
