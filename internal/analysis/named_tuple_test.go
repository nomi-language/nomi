package analysis_test

import "testing"

// A distinct type over a tuple is built from its elements by a call,
// `Pair(1, "x")`, and taken apart by the same form in a pattern,
// `Pair(n, s)`, as a struct is built and matched by its name. One argument
// is still the whole tuple, `Pair((1, "x"))`.
func TestNamedTuple_BuiltAndMatchedByItsName(t *testing.T) {
	_, errs := checkSourceWithStdlib(`type Pair (Int, String)

fn run() -> Int {
    p = Pair(1, "x")
    q = Pair((2, "y"))
    Pair(a, _) = q
    case p {
        Pair(n, "x") -> n + a
        Pair(n, _) -> n
    }
}
`)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects the named tuple forms: %v", errs)
	}
}

// Each element is checked against its element type.
func TestNamedTuple_ChecksEachElement(t *testing.T) {
	_, errs := checkSourceWithStdlib("type Pair (Int, String)\n\nfn run() -> Pair {\n    Pair(\"x\", 1)\n}\n")
	if len(errs) == 0 {
		t.Fatal("the front end accepts `Pair(\"x\", 1)`")
	}
	if got := errs[0].Message; got != "element 1 of Pair: expected Int, got String" {
		t.Fatalf("error = %q, want the element mismatch", got)
	}
}
