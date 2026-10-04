package rt_test

import (
	"math"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func TestRangeContainsFloatUsesIntervalComparisons(t *testing.T) {
	for _, inclusive := range []bool{false, true} {
		for _, start := range []float64{math.NaN(), math.Inf(-1), -1, 0, math.Inf(1)} {
			for _, end := range []float64{math.NaN(), math.Inf(-1), 0, 1, math.Inf(1)} {
				for _, n := range []float64{math.NaN(), math.Inf(-1), -1, 0, 1, math.Inf(1)} {
					r := rt.Range[float64]{Start: start, End: rt.Some(end), Inclusive: inclusive}
					want := n >= start && n < end
					if inclusive {
						want = n >= start && n <= end
					}
					if got := rt.RangeContainsFloat(r, n); got != want {
						t.Fatalf("%v..%v inclusive=%v contains %v: %v, want %v", start, end, inclusive, n, got, want)
					}
				}
			}
		}
	}
}
