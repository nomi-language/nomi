package collation_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/collation"
	"github.com/nomi-language/nomi/internal/stdformat"
)

// A comparison costs a bounded amount of work, counted rather than timed:
// its allocations, and the collation elements it builds for the two
// strings. Names in Swedish touch tailored letters with contractions (ä is
// a with a diaeresis, sorted after z), the common case for a tailoring.
func TestComparisonCost(t *testing.T) {
	d := cldrfull.Full()
	pairs := [][2]string{
		{"Öberg", "Åberg"}, {"Lindström", "Lindqvist"}, {"Andersson", "Andersén"},
		{"zebra", "Zebra"}, {"file10", "file9"}, {"Ärlig", "Ärligt"},
	}
	for _, tc := range []struct {
		spec     string
		maxAlloc float64
	}{
		{"en;3;false;1;false", 0},
		{"sv;3;false;1;false", 0},
		{"en;3;true;1;false", 0},
	} {
		c := stdformat.CollatorFor(d, tc.spec)
		c.Compare("warm", "up")
		ces := 0
		for _, p := range pairs {
			ces += len(c.CEs(nil, p[0])) + len(c.CEs(nil, p[1]))
		}
		allocs := testing.AllocsPerRun(200, func() {
			for _, p := range pairs {
				c.Compare(p[0], p[1])
			}
		}) / float64(len(pairs))
		t.Logf("%s: %.2f allocations and %.1f collation elements per comparison", tc.spec, allocs, float64(ces)/float64(len(pairs)))
		if allocs > tc.maxAlloc {
			t.Errorf("%s: %.2f allocations per comparison, want at most %.0f", tc.spec, allocs, tc.maxAlloc)
		}
	}
	_ = collation.Primary
}
