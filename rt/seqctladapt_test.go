package rt

import "testing"

// TestSeqAdapterCtlAllocatesNothingPerElement is
// TestSeqCtlAllocatesNothingPerElement's claim about the four-state signal.
//
// The same reasoning holds and the same reasoning is why it is repeated rather
// than assumed: a single element count cannot distinguish "allocates once per
// pipeline" (fine) from "allocates once per element" (a regression in the push
// protocol's main cost property), so the identical fold is driven over 1,000
// and 100,000 elements and the counts must be equal.
//
// Two assertions, because the second is weaker than seqctl_test.go's. `rt.Ctl`
// is a `uint8`, so `#(U, Ctl)` is two registers exactly as `(U, bool)` is and
// the signal is free — but an adapter needs one bit of per-Run state that a
// reduce does not: `ended`, which distinguishes "this stage finished" from "a
// downstream consumer refused an element". It is captured by the yield closure
// `src.Run` is called with, that closure escapes through an indirect call, and
// so the bit is heap-allocated: one allocation per Run, constant in the element
// count.
//
// It could be removed by hoisting the flag into the `Seq`'s own closure
// environment, where it would share the record that already holds `src` and `f`.
// That is not done, because it would make a `Seq` stateful across runs: the
// same pipeline consumed twice — or once from two goroutines — would share the
// bit. One allocation per pipeline is cheaper than a data race in the protocol.
//
// So the strict claim is element-independence, and the delta against the plain
// adapter is asserted as exactly one rather than zero. A change that boxed the
// signal, or moved the state per element, fails one of the two.
func TestSeqAdapterCtlAllocatesNothingPerElement(t *testing.T) {
	fr := NewFrame(nil)
	drain := func(s Seq[int64]) int64 {
		total := int64(0)
		s.Run(fr, func(fr *Frame, x int64) bool {
			total += x
			return true
		})
		return total
	}
	ctl := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			if drain(SeqMapCtl(intSeq(n), func(fr *Frame, x int64) (int64, Ctl) {
				if x%2 != 0 {
					return 0, CtlSkip
				}
				return x * 2, CtlEmit
			})) < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	plain := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			if drain(SeqMap(intSeq(n), func(fr *Frame, x int64) int64 { return x * 2 })) < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	small, large := ctl(1_000), ctl(100_000)
	if small != large {
		t.Errorf("SeqMapCtl allocated %.0f at 1k elements and %.0f at 100k: "+
			"%.4f allocation(s) per element, and the four-state signal must pay none",
			small, large, (large-small)/99_000)
	}
	base := plain(1_000)
	if small-base != 1 {
		t.Errorf("SeqMapCtl allocates %.0f where SeqMap allocates %.0f; the delta must be "+
			"exactly 1 — the `ended` bit — and rt.Ctl itself must be free", small, base)
	}
	t.Logf("SeqMapCtl: %.0f allocation(s) at 1k and %.0f at 100k, plain SeqMap %.0f",
		small, large, base)
}

// TestSeqAdapterCtlStopsTheSource pins that a `break` reaches the source rather
// than merely ending this stage, which is what makes a `break` over an unbounded
// source terminate at all. Counted, not timed — one per driver, because each has
// its own `return false` and a copy-paste that dropped one would still pass
// every value assertion.
func TestSeqAdapterCtlStopsTheSource(t *testing.T) {
	fr := NewFrame(nil)
	unbounded := func(asked *int) Seq[int64] {
		return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
			for i := int64(1); ; i++ {
				*asked++
				if *asked > 1_000 {
					panic("the stop never reached the source")
				}
				if !yield(fr, i) {
					return false
				}
			}
		}}
	}
	drain := func(s Seq[int64]) []int64 {
		var out []int64
		s.Run(fr, func(fr *Frame, x int64) bool {
			out = append(out, x)
			return true
		})
		return out
	}

	var mapAsked int
	got := drain(SeqMapCtl(unbounded(&mapAsked), func(fr *Frame, x int64) (int64, Ctl) {
		if x > 3 {
			return 0, CtlStop
		}
		return x * 10, CtlEmit
	}))
	if want := []int64{10, 20, 30}; !equalInt64s(got, want) {
		t.Errorf("SeqMapCtl over an unbounded source gave %v, want %v", got, want)
	}
	if mapAsked != 4 {
		t.Errorf("SeqMapCtl asked the source %d times, want 4", mapAsked)
	}

	var filterAsked int
	got = drain(SeqFilterCtl(unbounded(&filterAsked), func(fr *Frame, x int64) (bool, Ctl) {
		if x > 4 {
			return false, CtlStop
		}
		return x%2 == 0, CtlEmit
	}))
	if want := []int64{2, 4}; !equalInt64s(got, want) {
		t.Errorf("SeqFilterCtl over an unbounded source gave %v, want %v", got, want)
	}
	if filterAsked != 5 {
		t.Errorf("SeqFilterCtl asked the source %d times, want 5", filterAsked)
	}

	var takeAsked int
	got = drain(SeqTakeWhileCtl(unbounded(&takeAsked), func(fr *Frame, x int64) (bool, Ctl) {
		return x < 3, CtlEmit
	}))
	if want := []int64{1, 2}; !equalInt64s(got, want) {
		t.Errorf("SeqTakeWhileCtl over an unbounded source gave %v, want %v", got, want)
	}
	if takeAsked != 3 {
		t.Errorf("SeqTakeWhileCtl asked the source %d times, want 3", takeAsked)
	}

	var eachAsked int
	seen := 0
	SeqEachCtl(fr, unbounded(&eachAsked), func(fr *Frame, x int64) bool {
		if x > 2 {
			return false
		}
		seen++
		return true
	})
	if seen != 2 || eachAsked != 3 {
		t.Errorf("SeqEachCtl saw %d elements after asking %d times, want 2 after 3", seen, eachAsked)
	}
}

// TestSeqAdapterCtlPerFamilyRules is the table seqctladapt.go's header states,
// asserted one cell at a time.
//
// Three drivers share a Go signature and differ only in what they do with the
// value, so these cells are the only protection against a copy-paste that
// collapsed two of them. Every row is a pair differing in one input, which is
// what makes a wrongly-shared answer visible:
//
//	map        CtlEmitStop v -> emits v, then ends         (v is an element)
//	filter     CtlEmitStop T -> emits the item, then ends  (v is a decision)
//	filter     CtlEmit     F -> skips, carries on
//	take_while CtlEmit     F -> ends                       <- the one line that differs
func TestSeqAdapterCtlPerFamilyRules(t *testing.T) {
	fr := NewFrame(nil)
	src := func() Seq[int64] { return ListSeq(int64List(1, 2, 3, 4, 5)) }
	drain := func(s Seq[int64]) []int64 {
		var out []int64
		s.Run(fr, func(fr *Frame, x int64) bool {
			out = append(out, x)
			return true
		})
		return out
	}

	// map: the break value is the output element, so 999 appears where the
	// element would have.
	got := drain(SeqMapCtl(src(), func(fr *Frame, x int64) (int64, Ctl) {
		if x == 3 {
			return 999, CtlEmitStop
		}
		return x * 10, CtlEmit
	}))
	if want := []int64{10, 20, 999}; !equalInt64s(got, want) {
		t.Errorf("map CtlEmitStop gave %v, want %v (the value is an element)", got, want)
	}

	// filter: the break value is the decision, so the item appears and never the
	// bool. `true` keeps 3; `false` drops it. The pair is one input apart.
	got = drain(SeqFilterCtl(src(), func(fr *Frame, x int64) (bool, Ctl) {
		if x == 3 {
			return true, CtlEmitStop
		}
		return x%2 == 0, CtlEmit
	}))
	if want := []int64{2, 3}; !equalInt64s(got, want) {
		t.Errorf("filter CtlEmitStop true gave %v, want %v (the value is a decision)", got, want)
	}
	got = drain(SeqFilterCtl(src(), func(fr *Frame, x int64) (bool, Ctl) {
		if x == 3 {
			return false, CtlEmitStop
		}
		return x%2 == 0, CtlEmit
	}))
	if want := []int64{2}; !equalInt64s(got, want) {
		t.Errorf("filter CtlEmitStop false gave %v, want %v", got, want)
	}

	// The one line that separates filter from take_while: a plain `false`
	// decision skips in one and ends in the other. The same callback both sides.
	odd := func(fr *Frame, x int64) (bool, Ctl) { return x%2 != 0, CtlEmit }
	if got, want := drain(SeqFilterCtl(src(), odd)), []int64{1, 3, 5}; !equalInt64s(got, want) {
		t.Errorf("filter with a false decision gave %v, want %v (skip and carry on)", got, want)
	}
	if got, want := drain(SeqTakeWhileCtl(src(), odd)), []int64{1}; !equalInt64s(got, want) {
		t.Errorf("take_while with a false decision gave %v, want %v (end)", got, want)
	}

	// CtlSkip in a take_while skips without ending, which is the reason
	// `continue` is a separate state from a false decision.
	got = drain(SeqTakeWhileCtl(src(), func(fr *Frame, x int64) (bool, Ctl) {
		if x == 2 {
			return false, CtlSkip
		}
		return x < 4, CtlEmit
	}))
	if want := []int64{1, 3}; !equalInt64s(got, want) {
		t.Errorf("take_while CtlSkip gave %v, want %v (skip without ending)", got, want)
	}
}

// TestSeqAdapterCtlRunAnswer pins `completed || ended` — the answer a one-stage
// pipeline cannot observe.
//
// `Seq.Run` reports exhaustion rather than "nothing stopped me". A callback's
// break is this stage finishing, so it answers true; a downstream consumer
// refusing an element answers false. Backwards, and `concat(map(a, f), b)`
// silently stops at `a`.
func TestSeqAdapterCtlRunAnswer(t *testing.T) {
	fr := NewFrame(nil)
	broke := SeqMapCtl(ListSeq(int64List(1, 2, 3)), func(fr *Frame, x int64) (int64, Ctl) {
		if x == 2 {
			return 0, CtlStop
		}
		return x, CtlEmit
	})
	if !broke.Run(fr, func(fr *Frame, x int64) bool { return true }) {
		t.Error("a callback's break answered `stopped by a consumer`; it is this stage finishing")
	}
	whole := SeqMapCtl(ListSeq(int64List(1, 2, 3)), func(fr *Frame, x int64) (int64, Ctl) {
		return x, CtlEmit
	})
	if whole.Run(fr, func(fr *Frame, x int64) bool { return false }) {
		t.Error("a consumer refusing the first element answered `exhausted`")
	}
}

// TestSeqCtlTerminalsAndStages pins the per-operation meaning of the four
// signals in drop_while, flat_map, find, any? and all?: what `break v`'s value
// is for each, and that `continue` differs from a plain false answer.
func TestSeqCtlTerminalsAndStages(t *testing.T) {
	fr := NewFrame(nil)
	src := func() Seq[int64] { return ListSeq(int64List(1, 2, 3, 4, 5)) }
	drain := func(s Seq[int64]) []int64 {
		var out []int64
		s.Run(fr, func(fr *Frame, x int64) bool {
			out = append(out, x)
			return true
		})
		return out
	}
	at := func(n int64, c Ctl, v bool, rest func(int64) bool) func(*Frame, int64) (bool, Ctl) {
		return func(_ *Frame, x int64) (bool, Ctl) {
			if x == n {
				return v, c
			}
			return rest(x), CtlEmit
		}
	}
	below := func(n int64) func(int64) bool { return func(x int64) bool { return x < n } }

	// drop_while: the value is the drop decision.
	for _, tc := range []struct {
		name string
		f    func(*Frame, int64) (bool, Ctl)
		want []int64
	}{
		{"skip keeps dropping", at(1, CtlSkip, false, below(3)), []int64{3, 4, 5}},
		{"skip drops an element the predicate keeps", at(1, CtlSkip, false, below(1)), []int64{2, 3, 4, 5}},
		{"stop ends empty", at(2, CtlStop, false, below(3)), nil},
		{"emit-stop false keeps then ends", at(2, CtlEmitStop, false, below(3)), []int64{2}},
		{"emit-stop true drops then ends", at(2, CtlEmitStop, true, below(3)), nil},
	} {
		if got := drain(SeqDropWhileCtl(src(), tc.f)); !equalInt64s(got, tc.want) {
			t.Errorf("drop_while %s gave %v, want %v", tc.name, got, tc.want)
		}
	}

	// flat_map: the value is the element's expansion.
	expand := func(c Ctl) func(*Frame, int64) (*List[int64], Ctl) {
		return func(_ *Frame, x int64) (*List[int64], Ctl) {
			if x == 2 {
				return int64List(x, x), c
			}
			return int64List(x * 10), CtlEmit
		}
	}
	for _, tc := range []struct {
		c    Ctl
		want []int64
	}{
		{CtlSkip, []int64{10, 30, 40, 50}},
		{CtlStop, []int64{10}},
		{CtlEmitStop, []int64{10, 2, 2}},
	} {
		if got := drain(SeqFlatMapListCtl(src(), expand(tc.c))); !equalInt64s(got, tc.want) {
			t.Errorf("flat_map list %v gave %v, want %v", tc.c, got, tc.want)
		}
		seqExpand := func(fr *Frame, x int64) (Seq[int64], Ctl) {
			xs, c := expand(tc.c)(fr, x)
			return ListSeq(xs), c
		}
		if got := drain(SeqFlatMapCtl(src(), seqExpand)); !equalInt64s(got, tc.want) {
			t.Errorf("flat_map seq %v gave %v, want %v", tc.c, got, tc.want)
		}
	}

	big := func(x int64) bool { return x > 3 }
	// find: the value is the predicate's answer for this element.
	for _, tc := range []struct {
		name string
		f    func(*Frame, int64) (bool, Ctl)
		want Maybe[int64]
	}{
		{"skip passes over a match", at(4, CtlSkip, false, big), Some[int64](5)},
		{"stop finds nothing", at(2, CtlStop, false, big), None[int64]()},
		{"emit-stop true answers this element", at(2, CtlEmitStop, true, big), Some[int64](2)},
		{"emit-stop false finds nothing", at(2, CtlEmitStop, false, big), None[int64]()},
	} {
		if got := SeqFindCtl(fr, src(), tc.f); got != tc.want {
			t.Errorf("find %s gave %v, want %v", tc.name, got, tc.want)
		}
	}

	// any? accumulates False until an element matches.
	for _, tc := range []struct {
		name string
		f    func(*Frame, int64) (bool, Ctl)
		want bool
	}{
		{"skip passes over the only match", at(4, CtlSkip, false, func(x int64) bool { return x == 4 }), false},
		{"stop answers false", at(1, CtlStop, false, big), false},
		{"emit-stop true answers true", at(1, CtlEmitStop, true, big), true},
		{"emit-stop false answers false", at(1, CtlEmitStop, false, big), false},
	} {
		if got := SeqAnyCtl(fr, src(), tc.f); got != tc.want {
			t.Errorf("any %s gave %v, want %v", tc.name, got, tc.want)
		}
	}
	// all? accumulates True until an element fails.
	for _, tc := range []struct {
		name string
		f    func(*Frame, int64) (bool, Ctl)
		want bool
	}{
		{"skip passes over the only failure", at(4, CtlSkip, false, func(x int64) bool { return x != 4 }), true},
		{"stop answers true", at(3, CtlStop, false, below(3)), true},
		{"emit-stop true answers true", at(2, CtlEmitStop, true, below(2)), true},
		{"emit-stop false answers false", at(1, CtlEmitStop, false, below(9)), false},
	} {
		if got := SeqAllCtl(fr, src(), tc.f); got != tc.want {
			t.Errorf("all %s gave %v, want %v", tc.name, got, tc.want)
		}
	}
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func int64List(xs ...int64) *List[int64] {
	var out *List[int64]
	for i := len(xs) - 1; i >= 0; i-- {
		out = Cons(xs[i], out)
	}
	return out
}
