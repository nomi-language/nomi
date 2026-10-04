package rt

import (
	"slices"
	"testing"
)

func vectorItems[T any](v Vector[T]) []T {
	return v.Items[v.Start : v.Start+v.Len]
}

// A source that counts itself is built into one slice of exactly that size:
// the capacity is the count, so no append regrew it.
func TestSeqToVector_PreallocatesFromKnownCount(t *testing.T) {
	src := ListSeq(Cons[int64](1, Cons[int64](2, Cons[int64](3, Cons[int64](4, Cons[int64](5, nil))))))
	src.Count = func(*Frame) Maybe[int64] { return Some[int64](5) }
	v := SeqToVector(nil, src)
	if got := vectorItems(v); !slices.Equal(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("SeqToVector = %v, want [1 2 3 4 5]", got)
	}
	if cap(v.Items) != 5 {
		t.Fatalf("capacity %d, want 5: the known count was not used to size the slice", cap(v.Items))
	}
	if allocs := testing.AllocsPerRun(20, func() { SeqToVector(nil, src) }); allocs > 3 {
		// The slice, the yield closure and the slice header it captures. Growing
		// from empty to five elements would add three more.
		t.Fatalf("SeqToVector over a counted source allocated %v times, want at most 3", allocs)
	}
}

// A lazy chain declines known_count and still yields every element in order.
func TestSeqToVector_UncountedSourceGrows(t *testing.T) {
	src := SeqFilter(finiteSeq(10), func(_ *Frame, x int64) bool { return x%2 == 0 })
	if SeqKnownCount(nil, src).Tag != TagNone {
		t.Fatal("a filtered source answered known_count; this test needs one that declines")
	}
	v := SeqToVector(nil, src)
	if got := vectorItems(v); !slices.Equal(got, []int64{2, 4, 6, 8, 10}) {
		t.Fatalf("SeqToVector = %v, want [2 4 6 8 10]", got)
	}
}

// The walk stops where the source stops: a bounded prefix of an unbounded
// source materializes without draining it.
func TestSeqToVector_DrivesThroughEachWhile(t *testing.T) {
	asked := 0
	v := SeqToVector(nil, SeqTake(countingSeq(t, &asked), 3))
	if got := vectorItems(v); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("SeqToVector = %v, want [1 2 3]", got)
	}
	if asked != 3 {
		t.Fatalf("the source was asked for %d elements, want 3", asked)
	}
}

func TestSeqToVector_EmptyIsTheZeroVector(t *testing.T) {
	src := ListSeq[int64](nil)
	src.Count = func(*Frame) Maybe[int64] { return Some[int64](0) }
	v := SeqToVector(nil, src)
	if v.Len != 0 || v.Items != nil {
		t.Fatalf("SeqToVector over an empty source = %+v, want the zero Vector", v)
	}
}

// A count that is wrong (a user Discrete's steps_between feeds a Range's) costs
// a bounded reservation or a regrow, never an allocation of the size it names.
func TestSeqToVector_DistrustsAWrongCount(t *testing.T) {
	huge := finiteSeq(3)
	huge.Count = func(*Frame) Maybe[int64] { return Some[int64](1 << 50) }
	v := SeqToVector(nil, huge)
	if got := vectorItems(v); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("SeqToVector = %v, want [1 2 3]", got)
	}
	if cap(v.Items) > vectorPreallocCap {
		t.Fatalf("capacity %d exceeds the preallocation bound %d", cap(v.Items), vectorPreallocCap)
	}
	negative := finiteSeq(2)
	negative.Count = func(*Frame) Maybe[int64] { return Some[int64](-4) }
	if got := vectorItems(SeqToVector(nil, negative)); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("SeqToVector = %v, want [1 2]", got)
	}
}
