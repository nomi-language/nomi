package irbuild

import "testing"

// An instance of another file's generic type at a position the checker types
// and the program never annotates runs on the VM
// (testdata/sibling_generic_infer): a lambda parameter over a list of them,
// a generic function's argument, a Maybe's payload, and instances at the
// calling file's own struct.
//
// Two gaps closed together. The checker left the Origin off the type a
// generic enum's variant constructor returns, so `Tree.Node("a")` had no
// declaring file to resolve against; and projectGenericInstance knew only
// this file's templates. It now instantiates another file's template in that
// file's gen (foreignTemplateAt, instantiateForeign), with each type
// argument imported there. zz.nomi is lowered after tree.nomi and asks it for
// a new instance after its walk.
func TestIRSiblingGenericInfer_InferredInstancesOfAnotherFilesTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "names [a, -, b]\n" +
		"widths [1, 4]\n" +
		"first 5\n" +
		"Some(Span{start: 5, stop: 6})\n" +
		"points [1, 0, 4]\n" +
		"corner 2\n" +
		"Span{start: 1.5, stop: 2.5}\n"
	got := vmReference(fixture("sibling_generic_infer/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
