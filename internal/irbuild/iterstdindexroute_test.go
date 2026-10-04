package irbuild

// The stdlib index is a second route to an `Iter.` lowering, and only the
// order of implCall's arms closes it.
//
// The index files `impl Iter<T>`'s functions under `Iter.<name>`, so
// `byType["Iter.map"]` resolves to std's own lowered Nomi body. A call never
// reaches it because `iterCall` sits ABOVE `stdlibCall` in implCall and CLAIMS
// the call either way: it lowers it, or refuses it as `Iter without the std
// protocol`.
//
// That is a test and not a comment because the check for "X is the one place
// that decides Y" is an enumeration of the other routes to Y, not an audit of
// X's callers; a caller audit of `loadIter` passes with every index entry
// sitting there unreached. If the order changes, `Iter.map` in a user file
// that reaches a non-std `Iter` resolves through the index instead of being
// declined, and the type-identity check the anchor performs is bypassed.
//
// `iter_test.go` asserts that a no-anchor module refuses `Iter without the std
// protocol`. That assertion is only load-bearing if the index route EXISTS: if
// `byType` held no `Iter.` keys, the refusal would be the only possible outcome.
// So the anti-vacuity half is asserted here.

import (
	"sort"
	"strings"
	"testing"
)

// TestIterRoute_TheStdlibIndexIsASecondRoute pins that the index holds the
// `Iter.` owner functions, so the ordering that closes the route is known to be
// load-bearing rather than incidental.
func TestIterRoute_TheStdlibIndexIsASecondRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	idx := stdlibLowering()
	var iterKeys, seqKeys []string
	for k := range idx.byType {
		switch {
		case strings.HasPrefix(k, "Iter."):
			iterKeys = append(iterKeys, k)
		case strings.HasPrefix(k, "Seq."):
			seqKeys = append(seqKeys, k)
		}
	}
	sort.Strings(iterKeys)
	sort.Strings(seqKeys)

	// The ANTI-VACUITY half. A zero here means the route has closed by itself,
	// and then iter_test.go's refusal assertion is passing for free rather than
	// because ordering holds. Either way the reader needs to know which.
	if len(iterKeys) == 0 {
		t.Fatalf("the stdlib index holds NO `Iter.` keys, so the ordering that keeps loadIter's " +
			"anchor authoritative is not load-bearing. That is not a failure of the builder; it " +
			"makes this test vacuous, and iter_test.go's `Iter without the std protocol` " +
			"assertion passes for free. Re-derive which mechanism protects the anchor before " +
			"deleting this")
	}

	// And that the route reaches the OWNER FUNCTIONS specifically — the ones
	// iterCall claims — rather than only `Seq`'s two protocol members, which are
	// a different type and legitimately the index's business.
	for _, want := range []string{"Iter.map", "Iter.filter", "Iter.to_list", "Iter.reduce"} {
		if len(idx.byType[want]) == 0 {
			t.Fatalf("the stdlib index holds %d `Iter.` keys but not %q, so the population this "+
				"test describes has changed shape. Keys: %v", len(iterKeys), want, iterKeys)
		}
	}

	t.Logf("the stdlib index holds %d `Iter.` keys and %d `Seq.` keys, so an "+
		"`Iter.`-qualified call HAS a second route to a lowering and only implCall's order closes "+
		"it — iterCall claims every such call, lowering it or refusing it by name, strictly above "+
		"stdlibCall. Enumerated routes to a rt.Seq lowering: iterCall, ctrlflow.go's "+
		"iterOwnerCall (both gate on g.iter), and this index (closed by order). Keys: %v / %v",
		len(iterKeys), len(seqKeys), iterKeys, seqKeys)
}
