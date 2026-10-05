package rt

import (
	"testing"
)

// The terminal family's runtime contract, at the level an output comparison
// cannot reach.
//
// internal/irbuild's iter_terminals.nomi already pins the expected text
// absolutely against its golden record. What that comparison is structurally
// blind to is anything that does not change output: a terminal that drained
// its source would print the same answer for every finite case, and a
// per-element allocation shows up in no diff. So this file asserts the two things only a runtime
// test can see — how many elements the source was asked for, and how many
// allocations the drive cost — plus the answers themselves, spelled out, because
// two paths that agree on a wrong answer agree.

// countingSeq is an unbounded ascending source that records how many elements it
// was asked for. Unbounded on purpose: a terminal that fails to stop does not
// return a wrong count here, it does not return, and the guard turns that into
// a failure rather than a hang.
func countingSeq(t *testing.T, asked *int) Seq[int64] {
	return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(1); ; i++ {
			*asked++
			if *asked > 10_000 {
				t.Fatal("the source was never stopped: this terminal drains instead of short-circuiting")
			}
			if !yield(fr, i) {
				return false
			}
		}
	}}
}

// finiteSeq is 1..n.
func finiteSeq(n int64) Seq[int64] {
	return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for i := int64(1); i <= n; i++ {
			if !yield(fr, i) {
				return false
			}
		}
		return true
	}}
}

// TestTerminalAnswersArePinnedAbsolutely is the polarity guard.
//
// `SeqAny` is `!Run(|x| !pred(x))`, which type-checks with the two negations in
// either place, and three of the four wrong arrangements answer correctly for
// some input. So each terminal is asked four questions whose answers differ:
// a predicate matching in the middle, one matching nothing, one matching
// everything, and an empty source — the last being the one std reads out of
// `reduce`'s seed, and the whole claim of this lowering is that the seed is the
// same fact as "the source ran to exhaustion".
func TestTerminalAnswersArePinnedAbsolutely(t *testing.T) {
	fr := NewFrame(nil)
	gt := func(n int64) func(*Frame, int64) bool {
		return func(fr *Frame, x int64) bool { return x > n }
	}
	eq := func(n int64) func(*Frame, int64) bool {
		return func(fr *Frame, x int64) bool { return x == n }
	}
	four, empty := finiteSeq(4), finiteSeq(0)

	anyCases := []struct {
		what string
		src  Seq[int64]
		pred func(*Frame, int64) bool
		want bool
	}{
		{"middle", four, eq(3), true},
		{"nothing", four, gt(99), false},
		{"everything", four, gt(0), true},
		{"empty", empty, gt(0), false},
	}
	for _, c := range anyCases {
		if got := SeqAny(fr, c.src, c.pred); got != c.want {
			t.Errorf("SeqAny/%s = %v, want %v", c.what, got, c.want)
		}
	}

	allCases := []struct {
		what string
		src  Seq[int64]
		pred func(*Frame, int64) bool
		want bool
	}{
		{"all pass", four, gt(0), true},
		{"fails in the middle", four, eq(1), false},
		{"fails at the first element", four, gt(2), false},
		{"empty is vacuously true", empty, gt(0), true},
	}
	for _, c := range allCases {
		if got := SeqAll(fr, c.src, c.pred); got != c.want {
			t.Errorf("SeqAll/%s = %v, want %v", c.what, got, c.want)
		}
	}

	if got := SeqEmpty(fr, empty); got != true {
		t.Errorf("SeqEmpty over an empty source = %v, want true", got)
	}
	if got := SeqEmpty(fr, four); got != false {
		t.Errorf("SeqEmpty over 1..4 = %v, want false", got)
	}
	if got := SeqNotEmpty(fr, empty); got != false {
		t.Errorf("SeqNotEmpty over an empty source = %v, want false", got)
	}
	if got := SeqNotEmpty(fr, four); got != true {
		t.Errorf("SeqNotEmpty over 1..4 = %v, want true", got)
	}

	// A Maybe's Tag is what distinguishes None from the Go zero value, which is
	// neither Some nor None by construction (prelude.go reserves tag 0). So the
	// tag is asserted and not only the payload: `Maybe[int64]{}` has payload 0
	// too, and a caller that forgot to seed the local would produce it.
	if got := SeqFirst(fr, four); got.Tag != TagSome || got.Some != 1 {
		t.Errorf("SeqFirst over 1..4 = %+v, want Some(1)", got)
	}
	if got := SeqFirst(fr, empty); got.Tag != TagNone {
		t.Errorf("SeqFirst over an empty source = %+v, want None with a real tag", got)
	}
	if got := SeqFind(fr, four, gt(2)); got.Tag != TagSome || got.Some != 3 {
		t.Errorf("SeqFind(> 2) over 1..4 = %+v, want Some(3)", got)
	}
	if got := SeqFind(fr, four, gt(99)); got.Tag != TagNone {
		t.Errorf("SeqFind(> 99) over 1..4 = %+v, want None", got)
	}
	if got := SeqFind(fr, empty, gt(0)); got.Tag != TagNone {
		t.Errorf("SeqFind over an empty source = %+v, want None", got)
	}

	// `each` visits every element in order, and answers Unit. The order is
	// recorded rather than the count, because a terminal that walked the source
	// backwards would produce the same count.
	var seen []int64
	if got := SeqEach(fr, four, func(fr *Frame, x int64) Unit {
		seen = append(seen, x)
		return Unit{}
	}); got != (Unit{}) {
		t.Errorf("SeqEach = %+v, want Unit{}", got)
	}
	if len(seen) != 4 || seen[0] != 1 || seen[3] != 4 {
		t.Errorf("SeqEach visited %v, want 1 2 3 4 in order", seen)
	}
	if got := SeqCount(fr, four); got != 4 {
		t.Errorf("SeqCount over 1..4 = %d, want 4", got)
	}
	if got := SeqCount(fr, empty); got != 0 {
		t.Errorf("SeqCount over an empty source = %d, want 0", got)
	}
}

// TestShortCircuitingTerminalsStopTheSource is the half the program fixture
// can only observe indirectly.
//
// Every one of these terminals answers correctly whether or not it stops early
// — draining a finite source produces the same Bool. So the answer cannot be
// the assertion; the number of elements the source was asked for is. The source
// is unbounded, so a terminal that does not stop fails rather than running slow.
func TestShortCircuitingTerminalsStopTheSource(t *testing.T) {
	fr := NewFrame(nil)
	cases := []struct {
		what  string
		drive func(Seq[int64]) any
		asked int
	}{
		{"any? matching the first element", func(s Seq[int64]) any { return SeqAny(fr, s, func(fr *Frame, x int64) bool { return x >= 1 }) }, 1},
		{"any? matching the fourth", func(s Seq[int64]) any { return SeqAny(fr, s, func(fr *Frame, x int64) bool { return x >= 4 }) }, 4},
		{"all? failing at the third", func(s Seq[int64]) any { return SeqAll(fr, s, func(fr *Frame, x int64) bool { return x < 3 }) }, 3},
		{"empty?", func(s Seq[int64]) any { return SeqEmpty(fr, s) }, 1},
		{"not_empty?", func(s Seq[int64]) any { return SeqNotEmpty(fr, s) }, 1},
		{"first", func(s Seq[int64]) any { return SeqFirst(fr, s) }, 1},
		{"find at the fourth", func(s Seq[int64]) any { return SeqFind(fr, s, func(fr *Frame, x int64) bool { return x == 4 }) }, 4},
		{"take_while stopping at the fourth", func(s Seq[int64]) any {
			return SeqToList(fr, SeqTakeWhile(s, func(fr *Frame, x int64) bool { return x < 4 }))
		}, 4},
	}
	for _, c := range cases {
		asked := 0
		got := c.drive(countingSeq(t, &asked))
		if asked != c.asked {
			t.Errorf("%s asked the source for %d element(s), want exactly %d (answer was %v)",
				c.what, asked, c.asked, got)
		}
	}
}

// TestTakeWhileEndsItsOwnStreamRatherThanTheConsumers pins the one bookkeeping
// distinction take_while keeps, and the only observable it has.
//
// `take_while` answers the protocol's Bool, and the two ways it can finish must
// answer differently: the predicate saying stop means "my stream ended
// normally" (true), while the downstream consumer refusing an element means
// "someone above me stopped this" (false). Collapsing them into one answer is
// invisible in every list a pipeline produces and changes what a following
// `Iter.concat` does — `concat` drives its second source only when the first
// answered true (std/iter.nomi:439).
func TestTakeWhileEndsItsOwnStreamRatherThanTheConsumers(t *testing.T) {
	fr := NewFrame(nil)
	bounded := SeqTakeWhile(finiteSeq(10), func(fr *Frame, x int64) bool { return x < 4 })

	if got := bounded.Run(fr, func(fr *Frame, x int64) bool { return true }); got != true {
		t.Errorf("a take_while ended by its own predicate answered %v; true means "+
			"\"my stream ended\", which is what lets a following concat move on", got)
	}
	if got := bounded.Run(fr, func(fr *Frame, x int64) bool { return false }); got != false {
		t.Errorf("a take_while stopped by its CONSUMER answered %v, want false", got)
	}
	// The source exhausting is also "my stream ended".
	exhausting := SeqTakeWhile(finiteSeq(3), func(fr *Frame, x int64) bool { return true })
	if got := exhausting.Run(fr, func(fr *Frame, x int64) bool { return true }); got != true {
		t.Errorf("a take_while whose source ran out answered %v, want true", got)
	}
}

// TestTakeWhileBoolIsObservableThroughConcat is the previous test's property
// again, as a wrong list rather than a wrong Bool.
//
// Asserting `Run(...) == true` in isolation pins the property but does not show
// what it is for, and a property whose only test is "this Bool is true" is the
// kind that gets simplified away by the next reader — the collapse mutation
// looks like removing dead bookkeeping. `Iter.concat` is the one consumer that
// can see it: its whole body is `if Iter.each_while(a, yield) { each_while(b,
// yield) } else { False }` (std/iter.nomi:437), so a `take_while` that reported
// its own predicate-stop as "the consumer stopped me" makes concat skip its
// second source entirely.
//
// The rule is written out here rather than calling rt.SeqConcat (seqsrc.go),
// so the test states the exact rule it relies on. It asserts that std's concat
// rule, applied to this Bool, yields the right elements; it does not exercise
// std's own concat body.
func TestTakeWhileBoolIsObservableThroughConcat(t *testing.T) {
	fr := NewFrame(nil)
	// std/iter.nomi:437, transcribed.
	concat := func(a, b Seq[int64]) Seq[int64] {
		return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
			if a.Run(fr, yield) {
				return b.Run(fr, yield)
			}
			return false
		}}
	}
	// 1, 2, 3 from the bounded left side, then 100, 200 from the right.
	left := SeqTakeWhile(finiteSeq(10), func(fr *Frame, x int64) bool { return x < 4 })
	right := SeqMap(finiteSeq(2), func(fr *Frame, x int64) int64 { return x * 100 })

	var got []int64
	concat(left, right).Run(fr, func(fr *Frame, x int64) bool {
		got = append(got, x)
		return true
	})
	want := []int64{1, 2, 3, 100, 200}
	if len(got) != len(want) {
		t.Fatalf("concat(take_while(1..10, < 4), map(1..2, *100)) yielded %v, want %v; "+
			"a short list here means take_while reported its own predicate-stop as "+
			"\"my consumer stopped me\", so concat never drove its second source", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("concat yielded %v, want %v", got, want)
		}
	}
}

// TestDropWhileConsultsThePredicateOnlyForTheLeadingRun is the shape that makes
// `drop_while` different from `filter`, and the two produce the same list for
// every input where the dropped elements happen to be a prefix.
//
// `[1, 2, 3, 1] |> Iter.drop_while(|x| x < 3)` is `[3, 1]`: the trailing 1 is
// kept, because the predicate is never asked again once the run has ended. A
// version that kept testing produces `[3]` — a plausible-looking answer, and
// the reason the predicate's call count is asserted here as well as the list.
func TestDropWhileConsultsThePredicateOnlyForTheLeadingRun(t *testing.T) {
	fr := NewFrame(nil)
	calls := 0
	src := Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		for _, x := range []int64{1, 2, 3, 1} {
			if !yield(fr, x) {
				return false
			}
		}
		return true
	}}
	got := SeqToList(fr, SeqDropWhile(src, func(fr *Frame, x int64) bool {
		calls++
		return x < 3
	}))
	if got == nil || got.Len != 2 || got.Head != 3 || got.Tail == nil || got.Tail.Head != 1 {
		t.Errorf("drop_while(< 3) over [1, 2, 3, 1] = %v, want [3, 1]", got)
	}
	// Three: the two dropped elements and the one that ended the run.
	if calls != 3 {
		t.Errorf("the predicate was called %d time(s); it must be consulted only "+
			"for the leading run, which is 3 calls over [1, 2, 3, 1]", calls)
	}
}

// TestWhileAdaptersAreReplayable pins std/iter.nomi's own promise for the two
// adapters in seqterm.go: their per-run state (`ended`, `started`) lives in the
// run and not in the sequence value.
//
// State hoisted into SeqTakeWhile's or SeqDropWhile's closure passes every other
// test in this file and makes the second consumption of a bound pipeline differ.
func TestWhileAdaptersAreReplayable(t *testing.T) {
	fr := NewFrame(nil)
	for _, c := range []struct {
		what string
		seq  Seq[int64]
		want int64
	}{
		{"take_while", SeqTakeWhile(finiteSeq(10), func(fr *Frame, x int64) bool { return x < 4 }), 3},
		{"drop_while", SeqDropWhile(finiteSeq(10), func(fr *Frame, x int64) bool { return x < 8 }), 3},
	} {
		first, second := SeqCount(fr, c.seq), SeqCount(fr, c.seq)
		if first != c.want || second != c.want {
			t.Errorf("%s yielded %d then %d elements; a replayable sequence yields %d both times",
				c.what, first, second, c.want)
		}
	}
}

// TestTerminalsAllocateNothingPerElement is TestSeqAllocatesNothingPerElement's
// claim extended to every terminal and adapter in seqterm.go, and it is stated
// the same way: as independence of the element count rather than as an absolute
// number.
//
// A drive is entitled to a fixed number of allocations when the chain is built
// — one closure per stage, plus one for a captured accumulator — and the
// question is whether it pays any per element. Driven over 1,000 and 100,000
// elements; a single per-element allocation makes the second figure 99,000
// higher and no tolerance can hide it.
//
// Every terminal here is given a predicate that never fires, so each one drains
// its source and the count is a count of the whole walk rather than of the first
// element.
func TestTerminalsAllocateNothingPerElement(t *testing.T) {
	fr := NewFrame(nil)
	drives := []struct {
		what  string
		drive func(int64) int64
	}{
		{"any? over map, no match", func(n int64) int64 {
			if SeqAny(fr, SeqMap(finiteSeq(n), func(fr *Frame, x int64) int64 { return x * 3 }),
				func(fr *Frame, x int64) bool { return x < 0 }) {
				return 1
			}
			return 0
		}},
		{"all? over filter", func(n int64) int64 {
			if SeqAll(fr, SeqFilter(finiteSeq(n), func(fr *Frame, x int64) bool { return x%2 == 0 }),
				func(fr *Frame, x int64) bool { return x >= 0 }) {
				return 1
			}
			return 0
		}},
		{"find, no match", func(n int64) int64 {
			return SeqFind(fr, finiteSeq(n), func(fr *Frame, x int64) bool { return x < 0 }).Some
		}},
		{"count", func(n int64) int64 { return SeqCount(fr, finiteSeq(n)) }},
		{"each", func(n int64) int64 {
			var sum int64
			SeqEach(fr, finiteSeq(n), func(fr *Frame, x int64) Unit {
				sum += x
				return Unit{}
			})
			return sum
		}},
		{"count over take_while", func(n int64) int64 {
			return SeqCount(fr, SeqTakeWhile(finiteSeq(n), func(fr *Frame, x int64) bool { return true }))
		}},
		{"count over drop_while", func(n int64) int64 {
			return SeqCount(fr, SeqDropWhile(finiteSeq(n), func(fr *Frame, x int64) bool { return x < 2 }))
		}},
	}
	for _, d := range drives {
		measure := func(n int64) float64 {
			return testing.AllocsPerRun(5, func() {
				// The result is consumed through a data-dependent branch, so
				// the drive is not dead code the compiler may delete outright,
				// which would leave nothing to count.
				if d.drive(n) < 0 {
					t.Fatal("unreachable")
				}
			})
		}
		small, large := measure(1_000), measure(100_000)
		if small != large {
			t.Errorf("%s allocated %.0f at 1k elements and %.0f at 100k: %.4f allocation(s) "+
				"per element, and a terminal must pay none",
				d.what, small, large, (large-small)/99_000)
			continue
		}
		t.Logf("%s: %.0f allocation(s), independent of element count", d.what, small)
	}
}
