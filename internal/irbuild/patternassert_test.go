package irbuild

import (
	"strings"
	"testing"
)

// TestPatternAssert_BindingsAreReadAfterTheMatch keeps the fixture's reads,
// which are what a wrongly scoped binding would fail to compile against once the
// shape above is reachable.
func TestPatternAssert_BindingsAreReadAfterTheMatch(t *testing.T) {
	src := readFixture(t, "pattern_assert.nomi")
	for _, needle := range []string{
		"assert n = compute()",
		"assert n == 7",
		"doubled = first * 2",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("the escaping-binding guard %q is gone from pattern_assert.nomi; a\n"+
				"pattern whose locals were scoped to the match would still lower", needle)
		}
	}
}
