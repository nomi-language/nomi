package irbuild

import (
	"testing"
)

// Three defensive guards whose input is unreachable BY CONSTRUCTION, as a
// LEDGER rather than as prose. A reason of that kind is a claim with an expiry
// condition, so each one's construction is asserted here: the day it changes,
// this test fails LOUDLY instead of the reason quietly becoming false.
//
// This is deliberately not three separate tests. What is being pinned is one
// thing, "these three guards have no reachable input", and splitting it would
// let two rows keep passing while the third's reason expired unnoticed.
//
//	siblingequal.go: the RIVAL refusal, which refuses rather than picking
//	     when two files each provide `Equatable.equal` for one receiver.
//	     registerImpl's rule at a second site: never let ordering decide. The
//	     front end forecloses the input. EXPIRES if the derive-collision check or
//	     interface coherence is relaxed.
//
//	siblingequal.go: the result-is-Bool check. The analyzer rejects an
//	     impl function whose return type disagrees with the interface's, so an
//	     `Equatable.equal` returning anything but Bool cannot be declared.
//	     EXPIRES if that check is dropped.
//
//	listconcat.go: the `+` gate. Every other arithmetic operator on two
//	     lists is a front-end error, so nothing else reaches the concatenation
//	     arm. This one is "unreached in practice, live in principle": the
//	     POPULATION to watch is an `impl Subtract for List` (or Multiply,
//	     Divide, Modulo) appearing in std, which would make `xs - ys` checkable
//	     and would then need the gate to stop it concatenating.
func TestTypeQualMember_SurvivorPreconditionsStillHold(t *testing.T) {
	// Each is a front-end foreclosure: each source below must be
	// REJECTED, and the message is not asserted — only the rejection, because
	// the claim is about reachability and not about diagnostic text.
	for _, tc := range []struct{ name, src, mutant string }{
		{
			name:   "two Equatable impls for one type",
			mutant: "the rival refusal in siblingEqualCall",
			src: "struct P\n  x: Int\nend\n\n" +
				"impl Equatable for P\n  fn equal(a: P, b: P) -> Bool\n    a.x == b.x\n  end\nend\n\n" +
				"derive Equatable for P\n\nfn main()\n  _ = 1\nend\n",
		},
		{
			name:   "an Equatable equal that does not return Bool",
			mutant: "the result-is-Bool check in siblingEqualCall",
			src: "struct P\n  x: Int\nend\n\n" +
				"impl Equatable for P\n  fn equal(a: P, b: P) -> Int\n      _ = b\n    a.x\n  end\nend\n\n" +
				"fn main()\n  _ = 1\nend\n",
		},
		{
			name:   "subtraction on two lists",
			mutant: "the `+` gate in listConcat",
			src: "fn f(xs: List<Int>, ys: List<Int>) -> List<Int>\n  xs - ys\nend\n\n" +
				"fn main()\n  _ = f([], [])\nend\n",
		},
		{
			name:   "multiplication on two lists",
			mutant: "the `+` gate in listConcat",
			src: "fn f(xs: List<Int>, ys: List<Int>) -> List<Int>\n  xs * ys\nend\n\n" +
				"fn main()\n  _ = f([], [])\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AnalyzeSource("main", tc.src); err == nil {
				t.Fatalf("the front end now ACCEPTS this, so %s is reachable and needs a "+
					"test of its own rather than an unreachable classification", tc.mutant)
			}
		})
	}
}
