package analysis_test

import (
	"testing"
)

// The checker is the fence that makes `rt.Equal`'s short-name comparison
// safe, and nothing asserted it.
//
// `rt.Equal` compares a record descriptor's short name, so
// `alpha.Point{x: 1}` and `beta.Point{x: 1}` are equal there — which
// contradicts nominal identity. The reason that laxness is not a user-visible
// wrong answer is that no source can reach it: the checker compares the
// DECLARING origin and refuses the comparison before it runs. Both routes are
// covered here because they produce different diagnostics from different
// checks, and only the first one was ever quoted.
func TestNominalEquality_TwoModulesSameNameIsRefused(t *testing.T) {
	siblings := map[string]string{
		"alpha": "pub struct Point {\n  x: Int\n}\n\npub fn make(): Point {\n  Point{x: 1}\n}\n",
		"beta":  "pub struct Point {\n  x: Int\n}\n\npub fn make(): Point {\n  Point{x: 1}\n}\n",
	}

	// Route 1: the `==` operator. checker.go's equality arm compares origins.
	errs := buildProjectExpectingErrors(t,
		"import alpha\nimport beta\n\nfn main() {\n  b = alpha.make() == beta.make()\n}\n",
		siblings)
	assertErrorContains(t, errs, "equality type mismatch: alpha.Point vs beta.Point")

	// Route 2: an ARGUMENT position, which is how a map lookup reaches the
	// same comparison (`Map.get(m, otherModulesKey)`). A different check,
	// naming both origins the same way.
	errs = buildProjectExpectingErrors(t,
		"import alpha\nimport beta\n\nfn take(p: alpha.Point): Int {\n  p.x\n}\n\nfn main() {\n  n = take(beta.make())\n}\n",
		siblings)
	assertErrorContains(t, errs, "argument 1: expected alpha.Point, got beta.Point")
}
