package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// maxSubstWithoutCheckerTypes is how many type parameters of instances the
// programs TestIRGolden_RecordsMatchTheVM runs enter with no checker type
// beside their kind, by entry (substFrame, substCensus): each parameter of
// each instance of one program's lowering once. An entry that reads its
// instance's arguments off an annotation or an operand kind has none, and
// each count only goes down as entries read the checker's record instead.
var maxSubstWithoutCheckerTypes = map[substEntry]int{
	// A program's impl over a std container registered at a kind with no
	// expression behind it (a box's dispatch entry, a result's part).
	substImpl: 18,
	// Std instances the builder asks for at a kind no expression has: a
	// sort's comparator over an element kind (stdCompareInstanceSym), a
	// runtime Debug renderer's nested parts (stdInstDebugSym), a std
	// interface value's dispatch entry (stdProgThunk), and the instances
	// those, and an impl with no checker types, reach.
	// TestDerivedJsonContainerFields_RoundTrip's program enters one.
	substStd: 12,
}

// checkSubstCensus fails when an entry's count rises past its maximum, and
// logs one that fell. A fall is not a failure: the count depends on how
// much this process lowers, and a run that reads std's lowering from the
// artifact cache a first run wrote enters fewer instances (157 against 237
// when this was written). Lower a maximum from a run with a fresh cache
// (NOMI_ARTIFACT_CACHE set to an empty directory).
func checkSubstCensus(t *testing.T, counts map[substEntry]int) {
	t.Helper()
	var lines []string
	total := 0
	for e := substEntry(0); e < substEntries; e++ {
		n, max := counts[e], maxSubstWithoutCheckerTypes[e]
		total += n
		if n > 0 || max > 0 {
			lines = append(lines, fmt.Sprintf("%s: %d", e, n))
		}
		switch {
		case n > max:
			t.Errorf("%d type parameters of %s instances are entered with no checker type, and at most %d may: read the instance's arguments from the checker's record", n, e, max)
		case n < max:
			t.Logf("%d type parameters of %s instances are entered with no checker type: lower maxSubstWithoutCheckerTypes[%s] to %d", n, e, substEntryGoName(e), n)
		}
	}
	sort.Strings(lines)
	t.Logf("instance type parameters with no checker type: %d (%s)", total, strings.Join(lines, ", "))
}

// substEntryGoName is e's constant name, for a failure message.
func substEntryGoName(e substEntry) string {
	return [substEntries]string{
		substMono: "substMono", substNested: "substNested", substType: "substType",
		substImpl: "substImpl", substMethod: "substMethod", substIfaceImpl: "substIfaceImpl",
		substIfaceDefault: "substIfaceDefault", substStd: "substStd",
		substAlias: "substAlias", substRefinement: "substRefinement", substProbe: "substProbe",
	}[e]
}
