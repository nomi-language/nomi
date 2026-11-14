package rt

import (
	"math/big"
	"strings"
	"sync"
	"testing"
)

// sliceByClusters is String.slice as the whole split defines it: the oracle
// the indexed seek must agree with.
func sliceByClusters(s string, lo, hi int64) string {
	return joinClusters(graphemeClusters(s), lo, hi)
}

func joinClusters(clusters []string, lo, hi int64) string {
	lo = max(lo, 0)
	hi = min(hi, int64(len(clusters)))
	if lo >= hi {
		return ""
	}
	return strings.Join(clusters[lo:hi], "")
}

// graphemeSamples are strings whose clusters are not their bytes or code
// points, each short (walked per call) and long (indexed and kept), with
// cluster boundaries falling on and across the index's marks.
func graphemeSamples() map[string]string {
	base := map[string]string{
		"ascii":     "hello, world",
		"crlf":      "a\r\nb\r\n\r\nc\rd\n",
		"combining": "hélló ñ",
		"zwj":       "a\U0001F468‍\U0001F469‍\U0001F467b\U0001F469‍\U0001F4BB",
		// An odd run of regional indicators: the pairing depends on the
		// parity counted from the run's start, so a mark inside the run
		// must carry the segmenter state.
		"flags": strings.Repeat("\U0001F1EB", 77) + "x",
		"mixed": "ab\r\nć\U0001F1EF\U0001F1F5d\U0001F44D\U0001F3FDe",
	}
	out := map[string]string{"empty": ""}
	for name, s := range base {
		out[name] = s
		long := s
		for len(long) < 3*graphemeIndexMin {
			long += s
		}
		out[name+"-long"] = long
	}
	return out
}

func TestStringSliceMatchesTheWholeSplit(t *testing.T) {
	for name, s := range graphemeSamples() {
		clusters := graphemeClusters(s)
		n := int64(len(clusters))
		if got := StringLength(s); got != n {
			t.Errorf("%s: StringLength = %d, want %d", name, got, n)
		}
		for lo := int64(-2); lo <= n+2; lo++ {
			for hi := lo - 1; hi <= n+2; hi++ {
				got := StringSlice(s, SmallInt(lo), SmallInt(hi))
				if want := joinClusters(clusters, lo, hi); got != want {
					t.Fatalf("%s: StringSlice(%d, %d) = %q, want %q", name, lo, hi, got, want)
				}
			}
		}
	}
}

func TestStringSliceClampsBoundsPastInt64(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 70)
	for name, s := range graphemeSamples() {
		if got := StringSlice(s, SmallInt(1), BigIntOf(huge)); got != sliceByClusters(s, 1, 1<<62) {
			t.Errorf("%s: slice to 2^70 = %q", name, got)
		}
		if got := StringSlice(s, BigIntOf(new(big.Int).Neg(huge)), SmallInt(2)); got != sliceByClusters(s, 0, 2) {
			t.Errorf("%s: slice from -2^70 = %q", name, got)
		}
	}
}

// TestGraphemeScanIsLinear counts the work of reading a string one cluster
// at a time: a scan of n one-cluster slices does O(n) segmenter steps and
// byte checks in all, where walking from the start on each call did O(n²).
func TestGraphemeScanIsLinear(t *testing.T) {
	work := 0
	graphemeWorkHook = func(n int) { work += n }
	defer func() { graphemeWorkHook = nil }()
	const n = 20000
	for name, unit := range map[string]string{
		"ascii":     "a",
		"crlf":      "\r\n",
		"combining": "é",
		"zwj":       "\U0001F468‍\U0001F469",
	} {
		s := strings.Repeat(unit, n)
		work = 0
		for i := int64(0); i < n; i++ {
			if StringSlice(s, SmallInt(i), SmallInt(i+1)) != unit {
				t.Fatalf("%s: cluster %d is not %q", name, i, unit)
			}
			if StringLength(s) != n {
				t.Fatalf("%s: length changed", name)
			}
		}
		// One walk to build the index, then at most a mark's worth of steps
		// per slice.
		if limit := n + n*(graphemeMarkEvery+1); work > limit {
			t.Errorf("%s: %d units of work for %d slices, want at most %d", name, work, n, limit)
		}
		t.Logf("%s: %d units of work for %d slices of %d clusters", name, work, n, n)
	}
}

// TestGraphemeIndexesAcrossTasks slices distinct long strings from many
// goroutines at once, more strings than the table has slots, so entries are
// replaced while others read them. Run under -race.
func TestGraphemeIndexesAcrossTasks(t *testing.T) {
	samples := graphemeSamples()
	var wg sync.WaitGroup
	for g := range 4 * graphemeIndexSlots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name, s := range samples {
				s = strings.Repeat("x", g) + s // a distinct string per goroutine
				n := int64(len(graphemeClusters(s)))
				for i := int64(0); i < n; i += 7 {
					if got, want := StringSlice(s, SmallInt(i), SmallInt(i+3)), sliceByClusters(s, i, i+3); got != want {
						t.Errorf("%s/%d: slice(%d) = %q, want %q", name, g, i, got, want)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
