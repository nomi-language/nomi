package irbuild

import (
	"testing"
)

// TestBareCall_ABareNameThatIsNotARequirementIsUnaffected is the control the
// test above needs: `ifaceSiblingRequirementCall` is asked for EVERY unresolved
// bare call in an impl body, so an arm that answered unconditionally would
// swallow a genuine `call to an unlowered function` and this mechanism would
// look like a fix for something it never touched.
//
// KNOWN-PRESENT NEGATIVE: `missing` is declared nowhere, so the front end must
// reject it — which is itself the assertion. If the front end ever admits it,
// the builder's own key is the fallback and the test says which it got.
func TestBareCall_ABareNameThatIsNotARequirementIsUnaffected(t *testing.T) {
	const src = "interface Formatted {\n" +
		"  fn label(value: self): String\n" +
		"}\n\n" +
		"struct U {\n  name: String\n}\n\n" +
		"impl Formatted for U {\n  fn label(u: U): String {\n    missing(u)\n  }\n}\n\n" +
		"fn main() {\n  _ = Formatted.label(U{name: \"a\"})\n}\n"
	if _, err := AnalyzeSource("probe.nomi", src); err == nil {
		t.Fatal("the front end accepted a bare call to an undeclared name inside an impl " +
			"body; the builder's requirement resolver must then be the thing that " +
			"refuses it, and this test needs rewriting against that")
	}
}
