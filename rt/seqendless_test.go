package rt

import (
	"math"
	"testing"
)

// The `infinite?` answer, per source and per adapter, and the trap a
// terminal raises on it. The corpus pins the same through programs
// (tests/13-iterators-and-pipes/infinite_test.nomi and the failure fixture
// internal/irbuild/testdata/infinite_iter_traps.nomi); this pins the
// combinators the VM does not reach in every shape, and that asking drives
// nothing.

func intCmp(_ *Frame, a, b int64) Ordering {
	switch {
	case a < b:
		return Ordering{Tag: TagLess}
	case a > b:
		return Ordering{Tag: TagGreater}
	}
	return Ordering{Tag: TagEqual}
}

func TestSeqEndlessPerSourceAndAdapter(t *testing.T) {
	fr := NewFrame(nil)
	id := func(_ *Frame, x int64) int64 { return x }
	keep := func(_ *Frame, x int64) bool { return true }
	pair := func(a, b int64) int64 { return a + b }
	eq := func(a, b int64) bool { return a == b }
	finite, empty := ListCellSeq[int64, List[int64]](listOf([]int64{1, 2})), ListCellSeq[int64, List[int64]](nil)
	finite.Count = func(*Frame) Maybe[int64] { return Some[int64](2) }
	empty.Count = func(*Frame) Maybe[int64] { return Some[int64](0) }
	repeat := SeqRepeat[int64](7)
	unbounded := Range[int64]{Start: 1, End: None[int64]()}
	nearMax := Range[int64]{Start: math.MaxInt64 - 3, End: None[int64]()}
	next := func(_ *Frame, v int64) Maybe[int64] { return Some(v + 1) }
	step := func(_ *Frame, v, by int64) Maybe[int64] { return Some(v + by) }

	for _, c := range []struct {
		name string
		seq  Seq[int64]
		want string
	}{
		{"repeat", repeat, "Iter.repeat"},
		{"from", SeqFrom(0), "Iter.from"},
		{"iterate", SeqIterate[int64](1, id), "Iter.iterate"},
		{"iterate that can break", SeqIterateCtl[int64](1, func(_ *Frame, x int64) (int64, Ctl) { return x, CtlEmit }), ""},
		{"a list", finite, ""},
		{"map", SeqMap(repeat, id), "Iter.repeat"},
		{"map that can break", SeqMapCtl(repeat, func(_ *Frame, x int64) (int64, Ctl) { return x, CtlEmit }), ""},
		{"filter", SeqFilter(repeat, keep), "Iter.repeat"},
		{"drop", SeqDrop(repeat, 2), "Iter.repeat"},
		{"drop_while", SeqDropWhile(repeat, keep), "Iter.repeat"},
		{"with_index", SeqWithIndex(repeat, pair), "Iter.repeat"},
		{"scan", SeqScan(repeat, 0, func(_ *Frame, a, x int64) int64 { return a + x }), "Iter.repeat"},
		{"flat_map", SeqFlatMap(repeat, func(_ *Frame, x int64) Seq[int64] { return finite }), "Iter.repeat"},
		{"flat_map of finite into infinite", SeqFlatMap(finite, func(_ *Frame, x int64) Seq[int64] { return repeat }), ""},
		{"take", SeqTake(repeat, 3), ""},
		{"take_while", SeqTakeWhile(repeat, keep), ""},
		{"concat finite then infinite", SeqConcat(finite, repeat), "Iter.repeat"},
		{"concat infinite then finite", SeqConcat(repeat, finite), "Iter.repeat"},
		{"concat of finite", SeqConcat(finite, finite), ""},
		{"zip of two infinite", SeqZip(SeqFrom(0), repeat, pair), "Iter.from"},
		{"zip with a finite side", SeqZip(repeat, finite, pair), ""},
		{"cycle of a counted list", SeqCycle(finite), "Iter.cycle"},
		{"cycle of an empty list", SeqCycle(empty), ""},
		{"cycle of an uncounted source", SeqCycle(SeqFilter(finite, keep)), ""},
		{"cycle of an infinite source", SeqCycle(repeat), "Iter.repeat"},
		{"an unbounded range", RangeSeq(unbounded, intCmp, next), "Range.from"},
		{"an unbounded range near Int.max_value", RangeSeq(nearMax, intCmp, next), ""},
		{"step_by of an unbounded range", StepByRangeSeq(unbounded, int64(2), intCmp, step), "Range.step_by"},
		{"step_by that does not move", StepByRangeSeq(unbounded, int64(0), intCmp, step), ""},
		{"step_by down from the top", StepByRangeSeq(Range[int64]{Start: math.MaxInt64, End: None[int64]()}, int64(-1), intCmp, step), "Range.step_by"},
		{"step_by up near the top", StepByRangeSeq(nearMax, int64(1), intCmp, step), ""},
		{"an unbounded range from -1", RangeSeq(Range[int64]{Start: -1, End: None[int64]()}, intCmp, next), "Range.from"},
		{"an unbounded range from Int.min_value", RangeSeq(Range[int64]{Start: math.MinInt64, End: None[int64]()}, intCmp, next), "Range.from"},
		{"an unbounded range just below the threshold", RangeSeq(Range[int64]{Start: math.MaxInt64 - rangeEndlessMin, End: None[int64]()}, intCmp, next), "Range.from"},
		{"an unbounded range just above the threshold", RangeSeq(Range[int64]{Start: math.MaxInt64 - rangeEndlessMin + 1, End: None[int64]()}, intCmp, next), ""},
		{"an unbounded range from Int.max_value", RangeSeq(Range[int64]{Start: math.MaxInt64, End: None[int64]()}, intCmp, next), ""},
		{"step_by up from -1", StepByRangeSeq(Range[int64]{Start: -1, End: None[int64]()}, int64(1), intCmp, step), "Range.step_by"},
		{"step_by up from Int.min_value", StepByRangeSeq(Range[int64]{Start: math.MinInt64, End: None[int64]()}, int64(3), intCmp, step), "Range.step_by"},
		{"step_by down from -1", StepByRangeSeq(Range[int64]{Start: -1, End: None[int64]()}, int64(-2), intCmp, step), "Range.step_by"},
		{"step_by down from 0", StepByRangeSeq(Range[int64]{Start: 0, End: None[int64]()}, int64(-1), intCmp, step), "Range.step_by"},
		{"step_by down near Int.min_value", StepByRangeSeq(Range[int64]{Start: math.MinInt64 + 5, End: None[int64]()}, int64(-1), intCmp, step), ""},
		{"step_by down just above the threshold", StepByRangeSeq(Range[int64]{Start: math.MinInt64 + rangeEndlessMin, End: None[int64]()}, int64(-1), intCmp, step), "Range.step_by"},
		{"step_by down just below the threshold", StepByRangeSeq(Range[int64]{Start: math.MinInt64 + rangeEndlessMin - 1, End: None[int64]()}, int64(-1), intCmp, step), ""},
		{"step_by up just below the threshold", StepByRangeSeq(Range[int64]{Start: math.MaxInt64 - rangeEndlessMin, End: None[int64]()}, int64(1), intCmp, step), "Range.step_by"},
		{"step_by up just above the threshold", StepByRangeSeq(Range[int64]{Start: math.MaxInt64 - rangeEndlessMin + 1, End: None[int64]()}, int64(1), intCmp, step), ""},
	} {
		if got := SeqEndless(fr, c.seq); got != c.want {
			t.Errorf("%s: SeqEndless = %q, want %q", c.name, got, c.want)
		}
	}
	chunks := SeqChunks(repeat, 2)
	if got := SeqEndless(fr, chunks); got != "Iter.repeat" {
		t.Errorf("chunks: SeqEndless = %q", got)
	}
	if got := SeqEndless(fr, SeqChunks(repeat, 0)); got != "" {
		t.Errorf("chunks(0): SeqEndless = %q", got)
	}
	if got := SeqEndless(fr, SeqChunkBy(repeat, id, eq)); got != "Iter.repeat" {
		t.Errorf("chunk_by: SeqEndless = %q", got)
	}
}

func TestSeqTakeKnowsItsCount(t *testing.T) {
	fr := NewFrame(nil)
	counted := ListCellSeq[int64, List[int64]](listOf([]int64{1, 2}))
	counted.Count = func(*Frame) Maybe[int64] { return Some[int64](2) }
	for _, c := range []struct {
		name string
		seq  Seq[int64]
		want Maybe[int64]
	}{
		{"of an infinite source", SeqTake(SeqRepeat[int64](1), 3), Some[int64](3)},
		{"a negative count", SeqTake(SeqRepeat[int64](1), -2), Some[int64](0)},
		{"of a counted source", SeqTake(counted, 5), Some[int64](2)},
		{"of an uncounted source", SeqTake(intSeq(9), 5), None[int64]()},
	} {
		if got := SeqKnownCount(fr, c.seq); got != c.want {
			t.Errorf("take %s: known_count = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestSeqMustEndTrapsBeforeDriving: the trap names the terminal and the
// source, and asks the source for nothing.
func TestSeqMustEndTrapsBeforeDriving(t *testing.T) {
	fr := NewFrame(nil)
	asked := 0
	src := SeqRepeat[int64](1)
	run := src.Run
	src.Run = func(fr *Frame, yield func(*Frame, int64) bool) bool {
		asked++
		return run(fr, yield)
	}
	defer func() {
		e, ok := recover().(*Error)
		if !ok {
			t.Fatal("SeqMustEnd did not trap on an infinite source")
		}
		want := "`Iter.to_list` of an infinite iterator (`Iter.repeat`) never finishes; limit it with `Iter.take` or `Iter.take_while`"
		if e.Msg != want {
			t.Errorf("trap = %q, want %q", e.Msg, want)
		}
		if asked != 0 {
			t.Errorf("the source was asked %d times before the trap", asked)
		}
	}()
	SeqMustEnd(fr, SeqTake(src, 3), "Iter.to_list")
	SeqMustEnd(fr, SeqMap(src, func(_ *Frame, x int64) int64 { return x }), "Iter.to_list")
}
