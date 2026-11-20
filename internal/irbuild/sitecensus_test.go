package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// censusRatchet is what checkSiteCensus holds one census to: what its
// sites are of (`operator sites`), the classes it holds with the maximum
// of each, and what to do about a site past one.
type censusRatchet struct {
	what string
	held map[string]bool
	max  map[string]int
	fix  string
}

// checkSiteCensus fails when a held class's sites rise past its maximum,
// naming them, and when a held class has no maximum or a maximum names no
// held class; it logs one that fell. A fall is logged, not failed, as
// checkSubstCensus does, so the check never depends on what else the
// process lowered or read from the artifact cache. Each census's counts
// were the same with an empty artifact cache, with a warm one, and with
// none (NOMI_ARTIFACT_CACHE=off); lower a maximum from a run with
// NOMI_ARTIFACT_CACHE set to an empty directory.
func checkSiteCensus(t *testing.T, r censusRatchet, sites map[string][]string) {
	t.Helper()
	for class := range r.max {
		if !r.held[class] {
			t.Errorf("the maximum of %s holds %q, which is no class it holds: remove it", r.what, class)
		}
	}
	var lines []string
	for class := range sites {
		lines = append(lines, fmt.Sprintf("%s: %d", class, len(sites[class])))
	}
	for class := range r.held {
		n := len(sites[class])
		max, listed := r.max[class]
		switch {
		case !listed:
			t.Errorf("%s of %q have no maximum: add it at %d", r.what, class, n)
		case n > max:
			list := append([]string(nil), sites[class]...)
			sort.Strings(list)
			t.Errorf("%d %s are %q, and at most %d may be: %s:\n  %s",
				n, r.what, class, max, r.fix, strings.Join(list, "\n  "))
		case n < max:
			t.Logf("%d %s are %q: lower its maximum to %d", n, r.what, class, n)
		}
	}
	sort.Strings(lines)
	t.Logf("%s by class: %s", r.what, strings.Join(lines, ", "))
}
