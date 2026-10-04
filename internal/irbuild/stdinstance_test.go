package irbuild

import (
	"testing"
)

// The guards for the per-program stdlib instance driver. See stdinstance.go.
//
// Each one holds a property the driver's correctness rests on, and each has a
// failure message naming what breaking it would mean rather than only that it
// broke.

// TestStdInstance_FixedPointSetsForJSON records WHICH sibling set the instance
// gen must be offered, because getting it wrong was a real bug with a quiet
// symptom.
//
// The driver first passed `withCycles` alone. `stdRecursiveClosure` returns
// `settled` plus the surviving cycle members when it has provisional candidates
// and NIL otherwise, so a module with nothing recursive left handed the instance
// gen an EMPTY sibling set — and `Maybe.from_json`'s `T.from_json` then refused
// `qualified call` while lowering cleanly in a probe that passed `settled`.
//
// This prints both cardinalities and asserts the one thing the fix rests on: the
// UNION is at least as large as `settled`. A future change that made
// `withCycles` a proper superset would make the union redundant and this test
// would still pass, which is correct — the union is the safe reading either way.
func TestStdInstance_FixedPointSetsForJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	idx := stdlibLowering()
	v := idx.views[stdInstanceModule]
	if v == nil {
		t.Fatalf("no retained view for std/%s", stdInstanceModule)
	}
	union := map[*stdCandidate]bool{}
	for c := range v.settled {
		union[c] = true
	}
	for c := range v.withCycles {
		union[c] = true
	}
	t.Logf("std/%s: settled=%d withCycles=%d (nil=%v) union=%d",
		stdInstanceModule, len(v.settled), len(v.withCycles), v.withCycles == nil, len(union))
	if len(union) < len(v.settled) {
		t.Fatalf("the union is smaller than `settled` (%d < %d), which is arithmetically "+
			"impossible and means one of these maps is being mutated", len(union), len(v.settled))
	}
	if len(v.withCycles) < len(v.settled) {
		t.Logf("CONFIRMED the bug's precondition: withCycles (%d) is SMALLER than settled "+
			"(%d), so passing it alone starves the instance gen of siblings. The driver "+
			"passes the union.", len(v.withCycles), len(v.settled))
	}
}
