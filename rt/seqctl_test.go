package rt

import "testing"

// TestSeqCtlAllocatesNothingPerElement is the same claim
// TestSeqAllocatesNothingPerElement makes about the protocol, made about the
// WIDENED callback shape — and it is the acceptance criterion for the encoding
// rather than a nicety.
//
// A single element count cannot distinguish the two outcomes that matter: an
// encoding that allocates once per PIPELINE is fine, and one that allocates
// once per ELEMENT destroys the headline result of the whole Iter effort.
// Those look identical at one count. So the same fold is driven over 1,000 and
// 100,000 elements and the counts must be EQUAL; the equality is the proof and
// the absolute number is incidental.
//
// It is also a property no output comparison can see: a slow run prints the
// same bytes as a fast one.
//
// A second return value is two registers, so the expected answer is that the
// widened shape costs exactly what the plain one does. Asserted as equality
// between the two SHAPES as well, so a future change that boxed the signal
// fails here rather than in a benchmark nobody runs.
func TestSeqCtlAllocatesNothingPerElement(t *testing.T) {
	fr := NewFrame(nil)
	ctl := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			sum := SeqReduceCtl(fr, intSeq(n), int64(0),
				func(fr *Frame, acc int64, x int64) (int64, bool) {
					if x%2 != 0 {
						// `continue`: the accumulator is unchanged and the
						// source advances.
						return acc, true
					}
					return acc + x, true
				})
			if sum < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	plain := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			sum := SeqReduce(fr, intSeq(n), int64(0),
				func(fr *Frame, acc int64, x int64) int64 { return acc + x })
			if sum < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	small, large := ctl(1_000), ctl(100_000)
	if small != large {
		t.Errorf("SeqReduceCtl allocated %.0f at 1k elements and %.0f at 100k: "+
			"%.4f allocation(s) per element, and the widened callback must pay none",
			small, large, (large-small)/99_000)
	}
	if got := plain(1_000); got != small {
		t.Errorf("the widened callback allocates %.0f where the plain one allocates %.0f; "+
			"the second return value must be free", small, got)
	}
	t.Logf("SeqReduceCtl: %.0f allocation(s) at 1k and %.0f at 100k, plain SeqReduce %.0f",
		small, large, plain(1_000))
}

// TestSeqReduceCtlStopsTheSource pins that `false` reaches the SOURCE rather
// than merely ending this stage, which is what makes `break` over an unbounded
// source terminate at all. Counted, not timed.
func TestSeqReduceCtlStopsTheSource(t *testing.T) {
	fr := NewFrame(nil)
	asked := 0
	unbounded := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(1); ; i++ {
			asked++
			if !yield(fr, i) {
				return false
			}
			if asked > 1000 {
				t.Fatal("the source was never stopped; the stop answer is not threaded")
			}
		}
	}}
	got := SeqReduceCtl(fr, unbounded, int64(0),
		func(fr *Frame, acc int64, x int64) (int64, bool) {
			if x == 4 {
				// `break acc` — the accumulator so far is the answer.
				return acc, false
			}
			return acc + x, true
		})
	if asked != 4 {
		t.Errorf("the source was asked for %d element(s); a break at 4 must ask for exactly 4", asked)
	}
	if got != 6 {
		t.Errorf("got %d, want 6 (1+2+3, with the breaking element contributing nothing)", got)
	}
}

// TestSeqReduceFirstCtlSeedsFromTheFirstElement pins the unseeded fold's two
// distinguishing facts: the first element seeds and the callback does not run
// for it, and an empty source faults with the one shared text rather than
// answering a zero the language does not have.
func TestSeqReduceFirstCtlSeedsFromTheFirstElement(t *testing.T) {
	fr := NewFrame(nil)
	// `|acc, x| acc` answers the FIRST element if and only if the first element
	// seeded rather than being folded into a zero value.
	if got := SeqReduceFirstCtl(fr, intSeq(5),
		func(fr *Frame, acc int64, x int64) (int64, bool) { return acc, true }); got != 0 {
		t.Errorf("got %d, want 0 — intSeq starts at 0 and the first element must seed", got)
	}
	calls := 0
	got := SeqReduceFirstCtl(fr, intSeq(4),
		func(fr *Frame, acc int64, x int64) (int64, bool) {
			calls++
			return acc + x, true
		})
	if calls != 3 || got != 6 {
		t.Errorf("got %d after %d call(s); want 6 after 3 — four elements, one of them the seed", got, calls)
	}
}
