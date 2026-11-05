package irbuild

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Two lowerings on two goroutines each keep their own decline census: each
// program's hooks hear its own declines and no other's, and each Result
// records its own. Run under -race, a census or a hook held in package
// state fails here (the language server lowers a file while another
// lowering runs, TestLoweringFromAnalysis_LeavesTheAnalysisUnchanged).
func TestIRDecline_ConcurrentLoweringsKeepTheirOwnCensus(t *testing.T) {
	defer PlantDeclineForTest("planted_decline", "a planted decline")()
	// The stdlib's shared lowering is built once, before either starts, so
	// what races is the two programs' own lowerings.
	stdlibLowering()

	const rounds = 40
	type heard struct {
		observed map[string]string
		at       []*Decline
	}
	lowerOne := func(name string) (heard, map[string][]string, error) {
		src := fmt.Sprintf(`fn planted_decline(n: Int) -> Int { n }

fn %s(n: Int) -> Int {
  planted_decline(n) + 1
}

fn main() {
  _ = %s(1)
}
`, name, name)
		p, err := AnalyzeSource(name+".nomi", src)
		if err != nil {
			return heard{}, nil, fmt.Errorf("the front end rejects the fixture, so nothing below is tested: %w", err)
		}
		h := heard{observed: map[string]string{}}
		p.Declines = DeclineHooks{
			Observed: func(fn, reason string) { h.observed[fn] = reason },
			At:       func(d *Decline) { h.at = append(h.at, d) },
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			return heard{}, nil, err
		}
		return h, res.declines, nil
	}

	names := []string{"first_body", "second_body"}
	var wg sync.WaitGroup
	errs := make([][]string, len(names))
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				h, declines, err := lowerOne(name)
				if err != nil {
					errs[i] = append(errs[i], err.Error())
					return
				}
				if len(h.observed) != 1 || h.observed[name] != "a planted decline" {
					errs[i] = append(errs[i], fmt.Sprintf("the hooks heard %v; want only %s's planted decline", h.observed, name))
				}
				if len(h.at) != 1 || h.at[0].Fn != name || h.at[0].Line != 4 {
					var got []string
					for _, d := range h.at {
						got = append(got, fmt.Sprintf("%s at line %d", d.Fn, d.Line))
					}
					errs[i] = append(errs[i], fmt.Sprintf("the At hook heard %v; want only %s at line 4", got, name))
				}
				var recorded []string
				for path, list := range declines {
					for _, d := range list {
						recorded = append(recorded, path+": "+d)
					}
				}
				if len(recorded) != 1 || !strings.HasSuffix(recorded[0], ": "+name+": a planted decline") {
					errs[i] = append(errs[i], fmt.Sprintf("the Result records %v; want only %s's decline", recorded, name))
				}
			}
		}()
	}
	wg.Wait()
	for i, list := range errs {
		for _, e := range slices.Compact(list) {
			t.Errorf("%s: %s", names[i], e)
		}
	}
}
