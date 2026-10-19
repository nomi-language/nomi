package rt

import (
	"math"
	"testing"
)

// The List and Vector membership kernels compare each element once, in order,
// and stop at the first match: a hit at index i costs i+1 comparisons and a
// miss costs one per element. Counted through the comparator, which is the
// only thing either kernel calls.
func TestContainsKernelsCompareEachElementOnceAndStopAtTheFirstMatch(t *testing.T) {
	items := []int64{5, 7, 7, 9}
	var xs *List[int64]
	for i := len(items) - 1; i >= 0; i-- {
		xs = Cons(items[i], xs)
	}
	v := VectorOf(items)
	for _, tc := range []struct {
		x     int64
		found bool
		calls int
	}{
		{5, true, 1},
		{7, true, 2},
		{9, true, 4},
		{1, false, 4},
	} {
		calls := 0
		eq := func(a, b int64) bool { calls++; return a == b }
		if got := ListCellContains[int64](xs, tc.x, eq); got != tc.found || calls != tc.calls {
			t.Errorf("List contains %d: %v after %d comparisons, want %v after %d", tc.x, got, calls, tc.found, tc.calls)
		}
		calls = 0
		if got := VectorContains(v, tc.x, eq); got != tc.found || calls != tc.calls {
			t.Errorf("Vector contains %d: %v after %d comparisons, want %v after %d", tc.x, got, calls, tc.found, tc.calls)
		}
	}
	calls := 0
	never := func(a, b int64) bool { calls++; return true }
	if ListCellContains[int64, List[int64]](nil, 1, never) || VectorContains(Vector[int64]{}, 1, never) || calls != 0 {
		t.Errorf("an empty List or Vector compared %d times or answered true", calls)
	}
	// A tail view scans only its window.
	calls = 0
	if VectorContains(VectorTail(v), 5, func(a, b int64) bool { calls++; return a == b }) || calls != 3 {
		t.Errorf("a Vector tail found the dropped head or compared %d times, want 3", calls)
	}
}

// With rt's own equality, which Nomi's `==` is, a NaN element is found by NaN
// and 0.0 by -0.0.
func TestContainsKernelsUseNomiFloatEquality(t *testing.T) {
	nan := math.NaN()
	xs := Cons[any](1.0, Cons[any](nan, nil))
	if !ListCellContains[any](xs, any(nan), Equal) {
		t.Error("a List holding NaN does not contain NaN under rt.Equal")
	}
	if !VectorContains(VectorOf([]any{0.0}), any(math.Copysign(0, -1)), Equal) {
		t.Error("a Vector holding 0.0 does not contain -0.0 under rt.Equal")
	}
}

func TestBytesContains(t *testing.T) {
	data := Bytes("ab\x00\xff")
	for _, tc := range []struct {
		b    Byte
		want bool
	}{{'a', true}, {'b', true}, {0, true}, {0xff, true}, {'c', false}} {
		if got := BytesContains(data, tc.b); got != tc.want {
			t.Errorf("BytesContains(%q, %d) = %v, want %v", data, tc.b, got, tc.want)
		}
	}
	if BytesContains(Bytes(""), 0) {
		t.Error("empty Bytes contains 0")
	}
}
