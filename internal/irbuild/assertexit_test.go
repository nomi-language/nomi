package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestAssertExit_TheCheckerHasTwoSpellingsForOneTestBoundary is the guard that
// keeps isTestBoundary complete.
//
// The failure it stands in front of is not hypothetical: matching one of the two
// spellings cost an `assert` in every prompt case a boundary disagreement, and,
// when the builder still produced Go, cost a `try` in one a file that did not
// compile. Both were found by
// reading the checker's field instead of the builder's `g.inTest` flag, and both
// would come straight back if the checker grew a THIRD spelling.
//
// So this asserts over what the checker PRODUCES rather than over a list written
// here: every boundary stamped on an assertion in a program holding both shapes
// must be one isTestBoundary admits, and both shapes must actually be present —
// otherwise the assertion is vacuous.
func TestAssertExit_TheCheckerHasTwoSpellingsForOneTestBoundary(t *testing.T) {
	src := "//! assert doubled(3) == 6\n//\nfn doubled(n: Int): Int {\n  n * 2\n}\n\n" +
		"test \"a declaration\" {\n  assert doubled(4) == 8\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	fa := p.Entry().FA
	if fa == nil {
		t.Fatal("no analysis, so the boundary field cannot be read at all")
	}
	seen := map[string]bool{}
	for _, sym := range fa.References {
		if sym == nil || sym.Kind != analysis.SymbolAssertion || sym.Assertion == nil {
			continue
		}
		seen[sym.Assertion.Boundary] = true
	}
	boundaries := make([]string, 0, len(seen))
	for b := range seen {
		boundaries = append(boundaries, b)
	}
	sort.Strings(boundaries)
	if len(boundaries) != 2 {
		t.Fatalf("expected the two test-boundary spellings and got %d: %v", len(boundaries), boundaries)
	}
	var declaration, attached bool
	for _, b := range boundaries {
		if !isTestBoundary(b) {
			t.Errorf("isTestBoundary rejects %q, which the checker stamps on an assertion in a test case", b)
		}
		if strings.HasPrefix(b, "test \"") {
			declaration = true
		}
		if strings.HasPrefix(b, "attached test for ") {
			attached = true
		}
	}
	// Both shapes present, or the loop above proved nothing about the one that
	// was missing.
	if !declaration || !attached {
		t.Fatalf("the program did not exercise both spellings (declaration=%v attached=%v): %v",
			declaration, attached, boundaries)
	}
}

// TestAssertionSubjectSetsAgreeWithTheChecker is the tripwire on
// preludeSpec.assertionSubject.
//
// "Which enums may be an assertion's SHAPE subject" is written down twice: the
// checker's assertionSuccessType admits one by NAME, and the builder carries the
// flag. The builder side cannot be derived — `Fragment<T>` is also a two-variant
// prelude enum, so a shape test would admit `assert frag` and lower it as "holds
// when Static", a wrong answer held off only by a front-end check the builder
// never re-evaluates.
//
// Held BEHAVIOURALLY rather than against an exported list, which is the stronger
// of the two available guards here: it asks the front end whether `assert e`
// type-checks for a value of each spec's own type, so two lists that were both
// wrong could not agree their way past it. The failure text distinguishes the two
// directions, because they are different bugs.
func TestAssertionSubjectSetsAgreeWithTheChecker(t *testing.T) {
	// One program per spec, each binding a value of that spec's type and
	// asserting it. `Fragment` is reached through std/literals' own constructor
	// spelling so the program does not depend on the enum being in the prelude.
	sources := map[string]string{
		"Maybe":    "test \"t\" {\n  x: Maybe<Int> = Some(1)\n  assert x\n}\n",
		"Result":   "test \"t\" {\n  x: Result<Int, String> = Ok(1)\n  assert x\n}\n",
		"Fragment": "import {\n  std/literals.Fragment\n}\n\ntest \"t\" {\n  x: Fragment<Int> = Fragment.Dynamic(1)\n  assert x\n}\n",
		// `Outcome` is NOT an assertion subject, and this row is what holds
		// that against the CHECKER rather than against the spec's own flag.
		// Reached through its own constructor spelling for Fragment's reason:
		// the program must not depend on the enum being in the prelude, and
		// `Outcome` is an ordinary import.
		"Outcome": "import {\n  std/tasks.Outcome\n}\n\ntest \"t\" {\n  x: Outcome<Int> = Outcome.Completed(1)\n  assert x\n}\n",
	}
	checked := 0
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		src, known := sources[spec.nomi]
		if !known {
			t.Fatalf("no probe program for prelude spec %q; add one or this guard silently "+
				"stops covering the new spec", spec.nomi)
		}
		checked++
		_, err := AnalyzeSource("main", src)
		accepted := err == nil
		switch {
		case spec.assertionSubject && !accepted:
			t.Errorf("the builder lowers %s as a shape subject and the checker rejects it: %v\n"+
				"That is a construct the front end never sanctioned.", spec.nomi, err)
		case !spec.assertionSubject && accepted:
			t.Errorf("the checker accepts %s as an assertion subject and the builder refuses it.\n"+
				"Either set assertionSubject and lower it, or the refusal is silently costing "+
				"the tally a key it cannot name.", spec.nomi)
		}
	}
	if checked != len(preludeSpecs) {
		t.Fatalf("checked %d of %d specs", checked, len(preludeSpecs))
	}
	// A spec on each side of the flag, or it distinguishes nothing and the shape
	// test it replaced would have sufficed.
	yes := 0
	for i := range preludeSpecs {
		if preludeSpecs[i].assertionSubject {
			yes++
		}
	}
	if yes == 0 || yes == len(preludeSpecs) {
		t.Fatalf("%d of %d specs are shape subjects; the flag does no work here", yes, len(preludeSpecs))
	}
}
