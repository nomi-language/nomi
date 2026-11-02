package irbuild

import "testing"

// TestDistinctTupleEquality_ContainersCompareOnTheVM pins `==`, `!=`, Set
// membership and Map keys over containers of a distinct over a tuple. Each
// was BLOCKED ("the comparison operator `==`", or `Maybe<Pair>` outside the
// value domain) before irEqualityListKind and irRetainedPreludePayload
// admitted the element. The `tag-` lines run a hand-written Equatable whose
// answers differ from structural equality, so a comparison that bypassed it
// would print False there.
func TestDistinctTupleEquality_ContainersCompareOnTheVM(t *testing.T) {
	const want = "list-eq=True\n" +
		"list-ne=True\n" +
		"list-differs=False\n" +
		"nested-list=True\n" +
		"vector=True\n" +
		"set=True\n" +
		"set-has=True\n" +
		"map-key=one\n" +
		"map-eq=True\n" +
		"map-value=False\n" +
		"maybe=True\n" +
		"list-of-maybe=True\n" +
		"tuple=True\n" +
		"maybe-debug=Some(Pair(3, \"c\"))\n" +
		"tag-list=True\n" +
		"tag-list-ne=True\n" +
		"tag-maybe=True\n" +
		"tag-result=True\n" +
		"tag-set-size=1\n" +
		"tag-set-has=True\n" +
		"tag-map-key=three\n"
	if got := vmReference(fixture("distinct_tuple_equality.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}
