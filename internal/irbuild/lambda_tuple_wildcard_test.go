package irbuild

import "testing"

// TestPinned_LambdaTupleWildcard runs lambdas whose tuple parameter pattern
// holds a `_`: `|(_, v)|`, `|(k, _)|`, `|(_, _)|` and `|(_, (a, _))|`, over a
// Map and a List of tuples, in Iter.map, Iter.filter, Iter.reduce and
// Iter.each, and in a generic function. A wildcard binds no name, so the
// parameter's type comes from the checker's record of the whole pattern
// (FileAnalysis.LambdaPatternTypes), not from the names it binds.
func TestPinned_LambdaTupleWildcard(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"[1, 2]\n" +
		"[\"a\", \"b\"]\n" +
		"[0, 0]\n" +
		"[10, 20]\n" +
		"[\"x\", \"y\"]\n" +
		"[2, 5]\n" +
		"[(\"b\", 2)]\n" +
		"[(\"y\", 20)]\n" +
		"3\n" +
		"30\n" +
		"1\n" +
		"2\n" +
		"x\n" +
		"y\n" +
		"[1, 2]\n"
	got := vmReference(fixture("lambda_tuple_wildcard.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("a lambda with a wildcard in its tuple parameter did not run:\nwant stdout=%q\ngot  %s", want, got)
	}
}
