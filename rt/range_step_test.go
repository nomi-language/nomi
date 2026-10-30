package rt

import (
	"fmt"
	"testing"
)

// Range.step_by walks the way its step moves the start: down to the end
// for a step that moves it down, up for one that moves it up, and nowhere
// when the end lies the other way or the step does not move.
func TestStepByRangeSeqWalksTheWayItsStepGoes(t *testing.T) {
	fr := NewFrame(nil)
	step := func(_ *Frame, v, by int64) Maybe[int64] { return Some(v + by) }
	bounded := func(start, end int64, inclusive bool) Range[int64] {
		return Range[int64]{Start: start, End: Some(end), Inclusive: inclusive}
	}
	for _, tc := range []struct {
		r    Range[int64]
		by   int64
		want string
	}{
		{bounded(5, 1, true), -1, "[5 4 3 2 1]"},
		{bounded(5, 1, false), -1, "[5 4 3 2]"},
		{bounded(10, 0, false), -3, "[10 7 4 1]"},
		{bounded(10, 1, true), -3, "[10 7 4 1]"},
		{bounded(1, 5, true), -1, "[]"},
		{bounded(1, 5, true), 0, "[]"},
		{bounded(5, 1, true), 0, "[]"},
		{bounded(5, 1, true), 1, "[]"},
		{bounded(1, 5, true), 2, "[1 3 5]"},
		{bounded(3, 3, true), -1, "[3]"},
		{bounded(3, 3, false), -1, "[]"},
	} {
		got := fmt.Sprint(collectInt64s(fr, StepByRangeSeq(tc.r, tc.by, intCmp, step)))
		if got != tc.want {
			t.Errorf("step_by(%d to %d, inclusive %v, by %d) = %s, want %s",
				tc.r.Start, tc.r.End.Some, tc.r.Inclusive, tc.by, got, tc.want)
		}
	}
}
