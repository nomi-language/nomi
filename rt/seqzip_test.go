package rt

import (
	"runtime"
	"testing"
	"time"
)

// SeqZip's pull cursor, at the level no output comparison can reach.
//
// The golden files hold `13-iterators-and-pipes/iter_test.nomi`'s output byte
// for byte, and that file exercises every zip shape the language has:
// finite/finite, finite/shorter, unbounded/unbounded bounded by a `take`, one
// pair taken from two unbounded sources, and unbounded/finite.
//
// That comparison cannot see a leak. `iter.Pull` runs the pulled side on a
// coroutine, and a cursor abandoned without `stop()` leaves it parked forever.
// That changes no output — the answers are identical, the exit code is
// identical, and for an unbounded right operand the coroutine simply never
// returns. So `defer stop()` could be deleted and every test in this
// repository outside this file would stay green.
//
// So this test observes the goroutine count, which is a function of the
// cleanup and not of the answer.
func TestSeqZip_StoppingEarlyDoesNotStrandThePulledSide(t *testing.T) {
	// Two unbounded sources. Unbounded is what makes the leak permanent and
	// therefore countable: a finite right operand's coroutine exits on its own
	// when the source runs out, so it would hide a missing stop().
	from := func(start int64) Seq[int64] {
		return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
			for i := start; ; i++ {
				if !yield(fr, i) {
					return false
				}
			}
		}}
	}
	base := goroutinesSettled(t)
	for range 50 {
		zipped := SeqZip(from(0), from(100), func(a, b int64) [2]int64 { return [2]int64{a, b} })
		// One pair, then stop. This is
		// `Iter.zip(Iter.from(0), Iter.from(100)) |> Iter.first()`, the corpus
		// line whose comment says "stopping after one pair must not strand the
		// pulled side".
		got := [2]int64{}
		exhausted := zipped.Run(nil, func(fr *Frame, item [2]int64) bool {
			got = item
			return false
		})
		if got != [2]int64{0, 100} {
			t.Fatalf("first pair is %v, want [0 100]", got)
		}
		// A consumer stopped it, so the protocol's answer is false. Asserted
		// alongside the leak check because a `stop()` reached by returning the
		// wrong bool would be a different bug with the same goroutine count.
		if exhausted {
			t.Fatal("a consumer stopped the zip, so each_while must answer false")
		}
	}
	// 50 abandoned cursors. Without `defer stop()` this settles ~50 above base;
	// with it, back to base.
	if after := goroutinesSettled(t); after > base+2 {
		t.Fatalf("50 early-stopped zips left %d goroutines above baseline "+
			"(base %d, after %d). SeqZip's pull cursor is not being stopped, so "+
			"every abandoned zip over an unbounded right operand parks a "+
			"coroutine forever. No differential test can see this: the answers "+
			"and the exit code are identical either way.",
			after-base, base, after)
	}
}

// The right side running out first must report exhaustion, not a consumer stop.
//
// This is the middle row of rt.SeqZip's `completed || exhausted` table and the
// one that is easy to get wrong, because the implementation stops the left source
// by returning false into it — the same signal a consumer stop uses. Reporting
// `completed` alone would tell every enclosing adapter that somebody refused an
// element, which is the protocol's other meaning.
//
// The three rows are asserted together because the bug is a confusion between
// them, so any one row alone is satisfiable by a constant.
func TestSeqZip_TheAnswerDistinguishesExhaustionFromAConsumerStop(t *testing.T) {
	pair := func(a, b int64) [2]int64 { return [2]int64{a, b} }
	drain := func(s Seq[[2]int64]) (bool, int) {
		n := 0
		done := s.Run(nil, func(fr *Frame, _ [2]int64) bool { n++; return true })
		return done, n
	}
	three := ListSeq(listOf([]int64{1, 2, 3}))
	two := ListSeq(listOf([]int64{10, 20}))

	// Left runs out: completed.
	if done, n := drain(SeqZip(two, three, pair)); !done || n != 2 {
		t.Fatalf("left-exhausted zip: done=%v n=%d, want true/2", done, n)
	}
	// Right runs out first: still exhausted, because the zip is exhausted.
	if done, n := drain(SeqZip(three, two, pair)); !done || n != 2 {
		t.Fatalf("right-exhausted zip: done=%v n=%d, want true/2 — the zip "+
			"exhausted, so it must not report a consumer stop", done, n)
	}
	// A consumer stops: not exhausted.
	stopped := SeqZip(three, three, pair).Run(nil, func(fr *Frame, _ [2]int64) bool { return false })
	if stopped {
		t.Fatal("consumer-stopped zip reported exhaustion")
	}
}

// goroutinesSettled is the goroutine count once it stops moving, so a coroutine
// still unwinding is not counted as a leak.
//
// A positive settle rather than a fixed sleep: it returns only after two equal
// consecutive readings, and fails the test if the count never settles — so a
// flake reads as a failure with a reason rather than as a passing test that
// measured nothing.
func goroutinesSettled(t *testing.T) int {
	t.Helper()
	prev := -1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the goroutine count never settled (last %d), so this test cannot "+
		"distinguish a leak from noise", prev)
	return 0
}
