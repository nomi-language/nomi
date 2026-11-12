package irbuild

import "testing"

// Debug.inspect and Display of an instance of another file's generic type run
// on the VM (testdata/sibling_generic_debug): the universal Debug of a struct
// and an enum, a written Debug and a written Display, at two instantiations,
// directly and nested in a tuple, a record and a list.
//
// The caller holds a mirror of the instance, and its impls are registered in
// the declaring file's gen against that gen's own instance def. The lookup
// by declaration node that finds a non-generic mirror's impl cannot tell two
// instantiations apart, so a mirror of an instance asks the owner for the
// impl of its instance def (instanceImpl).
func TestIRSiblingGenericDebug_InstanceImplsAreTheOwnersInstances(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "Span{start: 1, stop: 2}\n" +
		"Span{start: \"a\", stop: \"b\"}\n" +
		"Node(3)\n" +
		"Leaf\n" +
		"tagged\n" +
		"#7\n" +
		"#x\n" +
		"(Span{start: 1, stop: 2}, 9)\n" +
		"{s: tagged}\n" +
		"[Span{start: 1, stop: 2}]\n" +
		"[Span{start: \"p\", stop: \"q\"}]\n" +
		"[#1]\n" +
		"[#s]\n"
	got := vmReference(fixture("sibling_generic_debug/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
