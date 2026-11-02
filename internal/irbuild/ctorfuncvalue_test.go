package irbuild

import "testing"

// Constructors named as function values (testdata/ctor_func_value): a
// distinct type (`Id`, a tuple-distinct `Pair`, another file's `ids.UserId`),
// the std variants bare and qualified (`Some`, `Maybe.Some`, `Ok`,
// `Result.Err`, `Err` under an annotated binding, `Some` inside a generic
// body), and a user enum's positional variants (`Shape.Circle`, `Shape.Seg`
// over a tuple, generic `Box.Full`). Each value is a function whose body is
// the constructor call, so what it builds is equal to, and renders as, the
// value the call builds directly.
func TestCtorFuncValue_BuildsWhatTheCallBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "[Id(1), Id(2)]\n" +
		"True\n" +
		"Id(3)\n" +
		"Id(4)\n" +
		"[Pair(1, \"a\")]\n" +
		"[UserId(5)]\n" +
		"True\n" +
		"[Some(1)]\n" +
		"True\n" +
		"[Some(1), Some(2)]\n" +
		"[Ok(1)]\n" +
		"True\n" +
		"Err(Label(\"bad\"))\n" +
		"Err(\"e\")\n" +
		"[Some(\"a\")]\n" +
		"Some(Circle(1.5))\n" +
		"[Seg(1, 2)]\n" +
		"[Full(1), Full(2)]\n" +
		"True\n"
	got := vmReference(fixture("ctor_func_value/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
