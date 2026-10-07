package irbuild

import "testing"

// Ordering and equality on types whose impls a sibling file writes run on the
// VM (testdata/sortscope): `Iter.sort` and `Iter.sort_by`, ascending and
// descending and stable, on two receivers of Comparable in one file; the
// comparison operators and `Comparable.compare` on one pair; `Ranked.compare`,
// a second interface's `compare` for the same receiver ordering the other
// way; the scalar operators beside them; and `==` through a hand-written
// sibling `impl Equatable`.
func TestIRSortScope_SiblingImplsOrderAndCompare(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "sort widget [1, 2, 3]\n" +
		"sort backwards [3, 2, 1]\n" +
		"sort_by widget [1, 2, 3]\n" +
		"sort widget desc [3, 2, 1]\n" +
		"sort_by widget desc [3, 2, 1]\n" +
		"stable [1, 1, 2]\n" +
		"lt True\n" +
		"gt False\n" +
		"le True\n" +
		"ge False\n" +
		"compare Less\n" +
		"str lt True\n" +
		"str ge True\n" +
		"bool lt True\n" +
		"bool le False\n" +
		"ranked Greater\n" +
		"tag x\n" +
		"eq True\n" +
		"ne False\n"
	got := vmReference(fixture("sortscope/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
