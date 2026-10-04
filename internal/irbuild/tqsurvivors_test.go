package irbuild

import (
	"strings"
	"testing"
	"unicode"
)

// Four defensive guards whose input is unreachable BY CONSTRUCTION, as a
// LEDGER rather than as prose. A reason of that kind is a claim with an expiry
// condition, so each one's construction is asserted here: the day it changes,
// this test fails LOUDLY instead of the reason quietly becoming false.
//
// This is deliberately not four separate tests. What is being pinned is one
// thing, "these four guards have no reachable input", and splitting it would
// let three rows keep passing while the fourth's reason expired unnoticed.
//
//	synthderive.go: the LOWERCASE (module-vs-type) guard on synthStdFileCall.
//	     Reached only for a synth-band `*ast.TypeIdent` whose
//	     `<name>.<method>` is a row in std's byFile table. No row has an
//	     uppercase qualifier, so a synthesized TYPE qualifier (`Map.get`,
//	     `Json.String`) cannot match one. EXPIRES if std gains a file whose name
//	     starts uppercase, or if byFile is ever keyed on something other than a
//	     file name, and it becomes load-bearing immediately if a row's qualifier
//	     ever collides with a type name derive synthesis also emits.
//
//	siblingequal.go: the RIVAL refusal, which refuses rather than picking
//	     when two files each provide `Equatable.equal?` for one receiver.
//	     registerImpl's rule at a second site: never let ordering decide. The
//	     front end forecloses the input. EXPIRES if the derive-collision check or
//	     interface coherence is relaxed.
//
//	siblingequal.go: the result-is-Bool check. The analyzer rejects an
//	     impl function whose return type disagrees with the interface's, so an
//	     `Equatable.equal?` returning anything but Bool cannot be declared.
//	     EXPIRES if that check is dropped.
//
//	listconcat.go: the `+` gate. Every other arithmetic operator on two
//	     lists is a front-end error, so nothing else reaches the concatenation
//	     arm. This one is "unreached in practice, live in principle": the
//	     POPULATION to watch is an `impl Subtract for List` (or Multiply,
//	     Divide, Modulo) appearing in std, which would make `xs - ys` checkable
//	     and would then need the gate to stop it concatenating.
func TestTypeQualMember_SurvivorPreconditionsStillHold(t *testing.T) {
	// synthStdFileCall's lowercase guard.
	idx := stdlibLowering()
	if len(idx.byFile) == 0 {
		t.Fatal("std's byFile table is empty, so the lowercase-guard claim is vacuous rather than true")
	}
	for key := range idx.byFile {
		qualifier := key
		if i := strings.Index(key, "."); i >= 0 {
			qualifier = key[:i]
		}
		if qualifier != "" && unicode.IsUpper([]rune(qualifier)[0]) {
			t.Errorf("std's byFile holds %q, whose qualifier %q starts uppercase. "+
				"synthStdFileCall's lowercase guard is now LOAD-BEARING rather than a fence: "+
				"a synthesized `Type.member` could match a file row. Give it a test of its own",
				key, qualifier)
		}
	}

	// The other three are front-end foreclosures. Each source below must be
	// REJECTED, and the message is not asserted — only the rejection, because
	// the claim is about reachability and not about diagnostic text.
	for _, tc := range []struct{ name, src, mutant string }{
		{
			name:   "two Equatable impls for one type",
			mutant: "the rival refusal in siblingEqualCall",
			src: "struct P {\n  x: Int\n}\n\n" +
				"impl Equatable for P {\n  fn equal?(a: P, b: P): Bool {\n    a.x == b.x\n  }\n}\n\n" +
				"derive Equatable for P\n\nfn main() {\n  _ = 1\n}\n",
		},
		{
			name:   "an Equatable equal? that does not return Bool",
			mutant: "the result-is-Bool check in siblingEqualCall",
			src: "struct P {\n  x: Int\n}\n\n" +
				"impl Equatable for P {\n  fn equal?(a: P, b: P): Int { _ = b;\n    a.x\n  }\n}\n\n" +
				"fn main() {\n  _ = 1\n}\n",
		},
		{
			name:   "subtraction on two lists",
			mutant: "the `+` gate in listConcat",
			src: "fn f(xs: List<Int>, ys: List<Int>): List<Int> {\n  xs - ys\n}\n\n" +
				"fn main() {\n  _ = f([], [])\n}\n",
		},
		{
			name:   "multiplication on two lists",
			mutant: "the `+` gate in listConcat",
			src: "fn f(xs: List<Int>, ys: List<Int>): List<Int> {\n  xs * ys\n}\n\n" +
				"fn main() {\n  _ = f([], [])\n}\n",
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
