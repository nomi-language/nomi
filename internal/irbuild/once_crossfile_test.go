package irbuild

import "testing"

// Unannotated `once` bindings read from another file, in every composite
// position (tuple, list, struct field, argument, case arm, map), through their
// owner (`Limits.next`, itself defined from `Limits.top`), and from a `once`
// declared below its first read. The entry is checked before lib.nomi, and a
// read used to see no type: `(n, help)` was rejected as `(Int, Unit)`.
func TestOnceCrossFile_InferredTypesReachEveryRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"(1, [\"a\", \"b\"])\n" +
		"[[\"a\", \"b\"]]\n" +
		"Box{items: [\"a\", \"b\"]}\n" +
		"2\n" +
		"(0, [\"a\", \"b\"])\n" +
		"{\"k\" => [\"a\", \"b\"]}\n" +
		"[\"n\", \"s\"]\n" +
		"([\"aa\", \"bb\"], Some(1), 11, ([\"a\", \"b\"], 10))\n"
	got := vmReference(fixture("once_crossfile/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("cross-file once reads:\nwant stdout=%q\ngot  %s", want, got)
	}
}
