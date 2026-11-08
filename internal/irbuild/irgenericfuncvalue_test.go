package irbuild

import "testing"

// A generic function named as a value runs as the instance the checker
// instantiated the reference at (testdata/generic_func_value): a same-file
// generic, a bounded one, another file's generic qualified and selectively
// imported, another file's monomorphic `fn`, `io.print` and `io.inspect`, an
// annotated binding, a non-generic parameter, a two-parameter generic, and a
// generic body naming a bounded generic at its own type parameter.
func TestIRGenericFuncValue_RunsTheInstantiatedInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "x\n" +
		"y\n" +
		"1\n" +
		"2\n" +
		"<(1, 2)>\n" +
		"3\n" +
		"4\n" +
		"[\"a\", \"a\"]\n" +
		"10\n" +
		"[(1, p), (2, q)]\n" +
		"6\n" +
		"7\n" +
		"(k, 8)\n" +
		"[<9>, <10>]\n"
	got := vmReference(fixture("generic_func_value/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
