package irbuild

import (
	"testing"
)

// `Bool`'s ordering has NO ROUTE through the stdlib index, which is the entire
// reason rt.BoolCompare and scalarorder.go exist. BOTH doors are asserted, so the
// stand-in cannot outlive its reason.
//
// Door one: `derive Comparable for Bool` on `pub enum Bool { embeds False; embeds
// True }` synthesizes a body over two `pub host type` singletons, outside the
// subset, so `stdCompareAt(kindBool, …)` answers nil — it is one of the four
// declarations stdlib.go:1877 already names as reaching the settling interlock and
// failing anyway. Door two: a `stdlibBindings` row cannot reach it either, because
// `stdCandidateFor`'s binding arm is `case fd == nil` (a `host fn`, whose only
// route is an rt symbol) and a derive-synthesized declaration carries a FuncDef.
//
// If either door opens, std becomes the route — compareResult asks `stdCompareAt`
// FIRST — and this arm silently stops being reached. That is the rule-(5) hazard
// exactly, so the precondition is a TEST rather than a comment: a comment saying
// "this expires when X" is a claim, and a test that fails when X happens is a
// mechanism.
func TestScalarOrder_BoolStillHasNoStdRoute(t *testing.T) {
	if _, bound := stdlibHostFuncs["bool.Bool.compare"]; bound {
		t.Fatal("`bool.Bool.compare` now has a stdlibBindings row. scalarorder.go's " +
			"scalarOrdered[tagBool] is a stand-in for exactly its absence and is " +
			"now unreachable — delete the entry and rt.BoolCompare with it, or say " +
			"which one wins")
	}
	if len(scalarOrdered) != 1 {
		t.Fatalf("scalarOrdered has %d entries; it is documented as Bool ALONE, and "+
			"every addition is a claim that std cannot serve that scalar either — "+
			"which is a decision, not a mechanical extension", len(scalarOrdered))
	}
	if _, ok := scalarOrdered[tagBool]; !ok {
		t.Fatal("scalarOrdered has lost its tagBool entry; `False < True` and " +
			"`Iter.sort` over a List<Bool> have no other route")
	}
}
