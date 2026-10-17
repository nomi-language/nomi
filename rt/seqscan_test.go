package rt

import "testing"

func collectInt64s(fr *Frame, s Seq[int64]) []int64 {
	var out []int64
	s.Run(fr, func(_ *Frame, x int64) bool {
		out = append(out, x)
		return true
	})
	return out
}

func int64sOf(fr *Frame, xs *List[int64]) []int64 {
	var out []int64
	ListCellEachWhile[int64, List[int64]](fr, xs, func(_ *Frame, x int64) bool {
		out = append(out, x)
		return true
	})
	return out
}

// TestSeqScan pins the running fold: one state per element, the seed not
// emitted, and each Run starting again from the seed.
func TestSeqScan(t *testing.T) {
	fr := NewFrame(nil)
	running := SeqScan(ListSeq(int64List(3, 1, 4)), 0, func(_ *Frame, acc, x int64) int64 { return acc + x })
	for run := 0; run < 2; run++ {
		if got := collectInt64s(fr, running); !equalInt64s(got, []int64{3, 4, 8}) {
			t.Fatalf("run %d: %v, want [3 4 8]", run, got)
		}
	}
	if got := collectInt64s(fr, SeqScan(ListSeq(int64List()), 7, func(_ *Frame, acc, x int64) int64 { return acc + x })); len(got) != 0 {
		t.Errorf("an empty source scanned to %v, want nothing", got)
	}
	// Laziness: a consumer that stops after two elements reads two.
	calls := 0
	counted := SeqScan(SeqFrom(1), 0, func(_ *Frame, acc, x int64) int64 {
		calls++
		return acc + x
	})
	if got := collectInt64s(fr, SeqTake(counted, 2)); !equalInt64s(got, []int64{1, 3}) || calls != 2 {
		t.Errorf("take 2 of a scan over an unbounded source: %v after %d calls, want [1 3] after 2", got, calls)
	}
}

// TestSeqScanCtl pins the four signals: `continue` emits nothing and keeps
// the state, `break v` emits v and ends, a bare `break` ends emitting
// nothing, and the Run answer is this stage finishing.
func TestSeqScanCtl(t *testing.T) {
	fr := NewFrame(nil)
	s := SeqScanCtl(ListSeq(int64List(1, 2, 3, 4, 5)), 0, func(_ *Frame, acc, x int64) (int64, Ctl) {
		switch x {
		case 2:
			return 0, CtlSkip
		case 4:
			return acc * 100, CtlEmitStop
		}
		return acc + x, CtlEmit
	})
	if got := collectInt64s(fr, s); !equalInt64s(got, []int64{1, 4, 400}) {
		t.Errorf("continue and break v: %v, want [1 4 400]", got)
	}
	stopped := SeqScanCtl(ListSeq(int64List(1, 2, 3)), 0, func(_ *Frame, acc, x int64) (int64, Ctl) {
		if x == 2 {
			return 0, CtlStop
		}
		return acc + x, CtlEmit
	})
	if got := collectInt64s(fr, stopped); !equalInt64s(got, []int64{1}) {
		t.Errorf("bare break: %v, want [1]", got)
	}
	if !stopped.Run(fr, func(*Frame, int64) bool { return true }) {
		t.Error("a callback's break answered `stopped by a consumer`; it is this stage finishing")
	}
}

// TestSeqMapReduceCells pins the outputs in order with the final state, the
// empty source's `#([], seed)`, and the signals.
func TestSeqMapReduceCells(t *testing.T) {
	fr := NewFrame(nil)
	type result struct {
		outs []int64
		acc  int64
	}
	pair := func(outs *List[int64], acc int64) result { return result{int64sOf(fr, outs), acc} }
	plain := SeqMapReduceCells[int64, int64, int64, List[int64]](fr, ListSeq(int64List(1, 2, 3)), 10,
		func(_ *Frame, acc, x int64) (int64, int64) { return acc * x, acc + x }, pair)
	if !equalInt64s(plain.outs, []int64{10, 22, 39}) || plain.acc != 16 {
		t.Errorf("map_reduce: %v, want {[10 22 39] 16}", plain)
	}
	empty := SeqMapReduceCells[int64, int64, int64, List[int64]](fr, ListSeq(int64List()), 5,
		func(_ *Frame, acc, x int64) (int64, int64) { return x, acc + 1 }, pair)
	if len(empty.outs) != 0 || empty.acc != 5 {
		t.Errorf("an empty source: %v, want {[] 5}", empty)
	}
	ctl := func(stopAt int64, stop Ctl) result {
		return SeqMapReduceCtlCells[int64, int64, int64, List[int64]](fr, ListSeq(int64List(1, 2, 3, 4, 5)), 0,
			func(_ *Frame, acc, x int64) (int64, int64, Ctl) {
				switch x {
				case 2:
					return 0, 0, CtlSkip
				case stopAt:
					return x * 10, acc + 1000, stop
				}
				return x, acc + x, CtlEmit
			}, pair)
	}
	if got := ctl(4, CtlEmitStop); !equalInt64s(got.outs, []int64{1, 3, 40}) || got.acc != 1004 {
		t.Errorf("continue and break v: %v, want {[1 3 40] 1004}", got)
	}
	if got := ctl(4, CtlStop); !equalInt64s(got.outs, []int64{1, 3}) || got.acc != 4 {
		t.Errorf("bare break: %v, want {[1 3] 4}", got)
	}
}
