package rt

import (
	"fmt"
	"math"
	"math/big"
	"testing"
)

// IntRangeSeq yields what RangeSeq does with std's Int `compare` and `next`,
// across int64's top and with bounds past int64.
func TestIntRangeSeq_MatchesTheGenericWalk(t *testing.T) {
	huge := IntFromBig(new(big.Int).Lsh(big.NewInt(1), 70))
	negHuge := IntNeg(huge)
	maxI := int64(math.MaxInt64)
	some := func(v Value) Maybe[Value] { return Some(v) }
	ranges := []Range[Value]{
		{Start: int64(0), End: some(int64(5))},
		{Start: int64(0), End: some(int64(5)), Inclusive: true},
		{Start: int64(5), End: some(int64(2))},
		{Start: int64(-2), End: some(int64(-2)), Inclusive: true},
		{Start: maxI - 2, End: some(maxI), Inclusive: true},
		{Start: maxI - 2, End: some(maxI)},
		{Start: maxI - 1, End: some(IntAdd(maxI, int64(2)))},
		{Start: int64(3), End: some(negHuge)},
		{Start: negHuge, End: some(IntAdd(negHuge, int64(3))), Inclusive: true},
		{Start: maxI - 1, End: None[Value]()},
	}
	cmp := func(_ *Frame, a, b Value) Ordering {
		switch IntCompare(a, b) {
		case -1:
			return Ordering{Tag: TagLess}
		case 1:
			return Ordering{Tag: TagGreater}
		}
		return Ordering{Tag: TagEqual}
	}
	next := func(_ *Frame, v Value) Maybe[Value] { return Some(IntAdd(v, int64(1))) }
	walk := func(s Seq[Value]) string {
		var out []string
		s.Run(nil, func(_ *Frame, v Value) bool {
			out = append(out, fmt.Sprint(v))
			return len(out) < 6
		})
		return fmt.Sprint(out)
	}
	for _, r := range ranges {
		got, want := walk(IntRangeSeq(r)), walk(RangeSeq(r, cmp, next))
		if got != want {
			t.Errorf("%v..%v (inclusive %v): got %s, want %s", r.Start, r.End, r.Inclusive, got, want)
		}
		steps := func(_ *Frame, a, b Value) Maybe[Value] {
			if IntCompare(a, b) > 0 {
				return Some[Value](int64(0))
			}
			return Some(IntSub(b, a))
		}
		if got, want := intRangeKnownCount(r), RangeKnownCount(nil, r, cmp, steps); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%v..%v (inclusive %v): count %v, want %v", r.Start, r.End, r.Inclusive, got, want)
		}
	}
}
