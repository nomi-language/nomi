package rt

import (
	"testing"
)

// The push protocol's runtime contract, at the level an output comparison
// cannot reach.
//
// Every behavioural claim about a pipeline is already pinned end to end by
// internal/irbuild's iter_pipeline.nomi against its golden record, byte for
// byte. What that comparison is structurally blind to is anything that does
// not change output: a per-element allocation here shows up in no diff. So the
// tests below assert the two
// properties the representation was chosen for and which are invisible from
// above — the per-element allocation count, and the fact that a bounded chain
// asks the source for exactly the elements it consumes.

func intSeq(n int64) Seq[int64] {
	return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(0); i < n; i++ {
			if !yield(fr, i) {
				return false
			}
		}
		return true
	}}
}

// TestSeqAllocatesNothingPerElement is the claim the representation rests on,
// and its failure is the hardest to notice: a silent fall-through to boxing
// shows up only as a performance regression, never as a wrong answer.
//
// Stated as independence of the element count rather than as an absolute
// number, because a pipeline is entitled to a fixed number of allocations when
// it is built — one closure per stage — and the question is whether it pays any
// per element. So the same chain is driven over 1,000 and over 100,000 elements
// and the counts must be equal. A per-element allocation of even one makes the
// second figure 99,000 higher; no tolerance can hide that.
//
// A streaming consumer, not `SeqToList`: to_list allocates a cons cell per
// element by construction, which is the collection's cost and not the
// protocol's.
func TestSeqAllocatesNothingPerElement(t *testing.T) {
	fr := NewFrame(nil)
	drive := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			var sum int64
			chain := SeqFilter(
				SeqMap(intSeq(n), func(fr *Frame, x int64) int64 { return x * 3 }),
				func(fr *Frame, x int64) bool { return x%2 == 0 })
			chain.Run(fr, func(fr *Frame, x int64) bool {
				sum += x
				return true
			})
			// Consumed, so the closure body is not dead code the compiler may
			// delete outright, which would leave nothing to count.
			if sum < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	small, large := drive(1_000), drive(100_000)
	if small != large {
		t.Errorf("a depth-2 chain allocated %.0f at 1k elements and %.0f at 100k: "+
			"%.4f allocation(s) per element, and the push protocol must pay none",
			small, large, (large-small)/99_000)
	}
	t.Logf("depth-2 chain: %.0f allocation(s), independent of element count", small)
}

// TestTakeStopsTheSource pins that the protocol's Bool is threaded all the way
// back to the source rather than merely respected by the stage that produced
// it.
//
// This is the property that makes an unbounded source usable at all, and the
// mutation that breaks it — having an adapter ignore what its consumer answered
// — turns `Iter.from(1) |> Iter.map(f) |> Iter.take(3)` from a three-element
// list into a program that does not terminate. Counted rather than timed: the
// source records how many elements it was asked for.
func TestTakeStopsTheSource(t *testing.T) {
	fr := NewFrame(nil)
	asked := 0
	unbounded := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(1); ; i++ {
			asked++
			if !yield(fr, i) {
				return false
			}
			if asked > 1000 {
				t.Fatal("the source was never stopped; the yield answer is not threaded")
			}
		}
	}}
	got := SeqToList(fr, SeqTake(SeqMap(unbounded,
		func(fr *Frame, x int64) int64 { return x * 2 }), 3))
	if asked != 3 {
		t.Errorf("the source was asked for %d element(s); take(3) must ask for exactly 3", asked)
	}
	if got == nil || got.Len != 3 || got.Head != 2 {
		t.Errorf("got %v, want a 3-element list starting at 2", got)
	}
}

// TestSeqIsReplayable pins std/iter.nomi's own promise: "binding
// `x = Iter.map(xs, f)` and consuming `x` multiple times yields consistent
// results".
//
// For `take` that is a statement about where its counter lives. A counter in
// SeqTake's closure over the sequence passes every other test in this file and
// yields nothing the second time round.
func TestSeqIsReplayable(t *testing.T) {
	fr := NewFrame(nil)
	bounded := SeqTake(SeqFrom(1), 3)
	first := SeqToList(fr, bounded)
	second := SeqToList(fr, bounded)
	if !ListEqual(first, second, Eq[int64]) {
		t.Errorf("consuming the same sequence twice gave %v then %v", first, second)
	}
	if first.Len != 3 {
		t.Errorf("take(3) yielded %d element(s)", first.Len)
	}
}

// TestTakeZeroNeverTouchesTheSource pins that `n <= 0` returns before the
// source is driven. It is observable through an unbounded source: a take(0) that drove it would
// not return.
func TestTakeZeroNeverTouchesTheSource(t *testing.T) {
	fr := NewFrame(nil)
	touched := false
	src := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		touched = true
		return true
	}}
	for _, n := range []int64{0, -1} {
		if !TakeEach(fr, src, n, func(fr *Frame, item int64) bool { return true }) {
			t.Errorf("take(%d) must report the stream ended", n)
		}
		if touched {
			t.Fatalf("take(%d) drove the source", n)
		}
	}
}

// TestListSeqAndCountAgreeWithTheProtocol pins the two List entry points
// against each other.
//
// `ListCount` is the `known_count` protocol's O(1) answer, which internal/irbuild
// substitutes for a fold whenever the source's static kind is a List. It is
// only sound while the cached `Len` equals the number of elements
// `each_while` actually yields, so the two are compared rather than each being
// tested alone.
func TestListSeqAndCountAgreeWithTheProtocol(t *testing.T) {
	fr := NewFrame(nil)
	for _, xs := range []*List[int64]{
		nil,
		Cons[int64](1, nil),
		Cons[int64](1, Cons[int64](2, Cons[int64](3, nil))),
	} {
		yielded := int64(0)
		if !ListEachWhile(fr, xs, func(fr *Frame, item int64) bool {
			yielded++
			return true
		}) {
			t.Error("an unstopped walk must report exhaustion")
		}
		if got := ListCount(xs); got != yielded {
			t.Errorf("ListCount said %d, each_while yielded %d", got, yielded)
		}
		if got := SeqToList(fr, ListSeq(xs)); !ListEqual(got, xs, Eq[int64]) {
			t.Errorf("round-tripping a list through the protocol gave %v, want %v", got, xs)
		}
	}
}

// TestFilterForwardsTheElementNotThePredicate is the one place `filter_each`
// and `map_each` differ beyond their types, and getting it wrong produces a
// program that type-checks: a filter that yielded its predicate's answer would
// emit `[True, True]` where std's `filter` yields the elements.
func TestFilterForwardsTheElementNotThePredicate(t *testing.T) {
	fr := NewFrame(nil)
	got := SeqToList(fr, SeqFilter(intSeq(5), func(fr *Frame, x int64) bool { return x > 2 }))
	want := Cons[int64](3, Cons[int64](4, nil))
	if !ListEqual(got, want, Eq[int64]) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --- reduce -----------------------------------------------------------------

// TestSeqReduceFoldsFromTheSeed asserts the answers absolutely, which is the
// only kind of assertion that catches this bug class.
//
// The seed's whole failure mode is that a dropped seed is a Go zero value —
// right for every fold that starts at 0, "" or nil. So each case below starts
// somewhere the fold cannot reach on its own, and the `want` is spelled out.
func TestSeqReduceFoldsFromTheSeed(t *testing.T) {
	fr := NewFrame(nil)
	add := func(fr *Frame, acc, x int64) int64 { return acc + x }

	if got := SeqReduce(fr, intSeq(4), int64(100), add); got != 106 {
		t.Errorf("0+1+2+3 from a seed of 100 is %d, want 106", got)
	}
	// The line a dropped seed gets right, kept beside the one it gets wrong so
	// a reader can see what the pin is distinguishing.
	if got := SeqReduce(fr, intSeq(4), int64(0), add); got != 6 {
		t.Errorf("0+1+2+3 from a seed of 0 is %d, want 6", got)
	}
	// An empty source: the callback never runs, so the seed is the answer.
	// With a zero seed this case is indistinguishable from a broken one.
	if got := SeqReduce(fr, intSeq(0), int64(42), add); got != 42 {
		t.Errorf("reducing nothing from a seed of 42 is %d, want 42", got)
	}
	// A non-scalar accumulator, where the zero value is nil and prints the
	// same as a correct fold that started empty.
	got := SeqReduce(fr, intSeq(3), Cons[int64](9, nil),
		func(fr *Frame, acc *List[int64], x int64) *List[int64] { return Cons(x, acc) })
	want := Cons[int64](2, Cons[int64](1, Cons[int64](0, Cons[int64](9, nil))))
	if !ListEqual(got, want, Eq[int64]) {
		t.Errorf("folding onto a seed list gave %v, want %v", got, want)
	}
	// A String accumulator: a dropped seed loses the prefix and nothing else.
	if s := SeqReduce(fr, intSeq(3), "seed:",
		func(fr *Frame, acc string, x int64) string { return acc + FormatInt(x) }); s != "seed:012" {
		t.Errorf("folding strings from %q gave %q, want %q", "seed:", s, "seed:012")
	}
}

// TestSeqReduceConsumesTheWholeSource pins that reduce never stops early.
//
// Under the push protocol a consumer stops by answering false, and this one
// never does: a callback that says `break` is refused at the call site
// (internal/irbuild/iter.go), so the only thing that can end the loop is the
// source. Counted rather than inferred from the answer, because a fold whose
// last elements were skipped can still produce a plausible number.
func TestSeqReduceConsumesTheWholeSource(t *testing.T) {
	fr := NewFrame(nil)
	asked := 0
	counting := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(1); i <= 5; i++ {
			asked++
			if !yield(fr, i) {
				return false
			}
		}
		return true
	}}
	got := SeqReduce(fr, counting, int64(0), func(fr *Frame, acc, x int64) int64 { return acc + x })
	if asked != 5 || got != 15 {
		t.Errorf("reduce asked for %d element(s) and answered %d; want 5 and 15", asked, got)
	}
}

// TestSeqReduceFirstSeedsFromTheFirstElement pins the other program, and the
// two halves that distinguish it from a seeded fold.
//
// Both answers are read off `nomi run` rather than derived: `Iter.reduce([4, 5,
// 6], |acc, x| acc)` is 4 and `|acc, x| x` is 6, so the first element really
// does seed rather than being folded into a zero.
func TestSeqReduceFirstSeedsFromTheFirstElement(t *testing.T) {
	fr := NewFrame(nil)
	src := func() Seq[int64] {
		return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
			for _, v := range []int64{4, 5, 6} {
				if !yield(fr, v) {
					return false
				}
			}
			return true
		}}
	}
	if got := SeqReduceFirst(fr, src(), func(fr *Frame, acc, x int64) int64 { return acc }); got != 4 {
		t.Errorf("keeping the accumulator gave %d; the FIRST element must seed it, so want 4", got)
	}
	if got := SeqReduceFirst(fr, src(), func(fr *Frame, acc, x int64) int64 { return x }); got != 6 {
		t.Errorf("taking the element gave %d, want 6", got)
	}
	// The callback must not run at all for a single-element source: the answer
	// is the element itself.
	one := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		return yield(fr, 7)
	}}
	ran := 0
	if got := SeqReduceFirst(fr, one, func(fr *Frame, acc, x int64) int64 { ran++; return acc + x }); got != 7 || ran != 0 {
		t.Errorf("a single element gave %d with %d callback run(s); want 7 and 0", got, ran)
	}
}

// TestSeqReduceFirstTrapsOnAnEmptySource pins the fault text character for
// character.
//
// It is Nomi-observable — `nomi run` prints it to stderr and exits 1 — and it
// has exactly one encoding, so a golden file recorded from a wrong string
// would agree with it. Hence an absolute pin here.
func TestSeqReduceFirstTrapsOnAnEmptySource(t *testing.T) {
	const want = "Iter.reduce: cannot reduce empty collection without initial value"
	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok {
			t.Fatalf("an unseeded reduce over an empty source must trap with *rt.Error, got %#v", r)
		}
		if e.Msg != want {
			t.Errorf("trap text is %q, want %q", e.Msg, want)
		}
	}()
	SeqReduceFirst(NewFrame(nil), intSeq(0), func(fr *Frame, acc, x int64) int64 { return acc })
	t.Fatal("an unseeded reduce over an empty source must trap and did not")
}

// TestSeqReduceAllocatesNothingPerElement is TestSeqAllocatesNothingPerElement's
// claim for the terminal that carries state.
//
// The reason it needs its own test is that reduce is the first consumer with an
// accumulator: `acc` is closed over by the yield function, so Go must heap it,
// and a naive implementation could easily allocate one per element instead of
// one per call. Stated the same way for the same reason — as independence of
// the element count, because a fixed per-call cost is legitimate and a
// per-element one is not. A single allocation per element makes the second
// figure 99,000 higher.
func TestSeqReduceAllocatesNothingPerElement(t *testing.T) {
	fr := NewFrame(nil)
	drive := func(n int64) float64 {
		return testing.AllocsPerRun(5, func() {
			sum := SeqReduce(fr,
				SeqMap(intSeq(n), func(fr *Frame, x int64) int64 { return x * 3 }),
				int64(100), func(fr *Frame, acc, x int64) int64 { return acc + x })
			// Consumed, so the fold is not dead code the compiler may delete.
			if sum < 0 {
				t.Fatal("unreachable")
			}
		})
	}
	small, large := drive(1_000), drive(100_000)
	if small != large {
		t.Errorf("a seeded reduce over a mapped source allocated %.0f at 1k elements and %.0f at 100k: "+
			"%.4f allocation(s) per element, and the push protocol must pay none",
			small, large, (large-small)/99_000)
	}
	t.Logf("seeded reduce over a depth-1 chain: %.0f allocation(s), independent of element count", small)
}
