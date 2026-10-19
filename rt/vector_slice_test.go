package rt

import "testing"

// A vector pattern's `..rest` is a window over the vector it takes apart, so
// a recursive `#[x, ..rest]` walk over n elements reads each element once and
// copies none: the n slices it takes allocate nothing and share the
// vector's backing store. Counted, not timed.
func TestVectorSliceSharesItsStorage(t *testing.T) {
	const n = 1000
	items := make([]any, n)
	for i := range items {
		items[i] = int64(i)
	}
	v := VectorOf(items)

	steps := 0
	allocs := testing.AllocsPerRun(10, func() {
		steps = 0
		rest := v
		for rest.Len > 0 {
			rest = VectorSlice(rest, 1, 0)
			steps++
		}
	})
	if steps != n {
		t.Fatalf("the walk took %d steps over %d elements", steps, n)
	}
	if allocs != 0 {
		t.Errorf("slicing a vector %d times allocated %v times; a slice is a window over the same storage", n, allocs)
	}

	mid := VectorSlice(v, 2, 3)
	if mid.Len != n-5 || &mid.Items[mid.Start] != &v.Items[2] {
		t.Errorf("VectorSlice(v, 2, 3) is %d elements from %p, want %d elements from %p",
			mid.Len, &mid.Items[mid.Start], n-5, &v.Items[2])
	}
	if got := VectorAt(mid, int64(mid.Len-1)); got != int64(n-4) {
		t.Errorf("the slice's last element is %v, want %d", got, n-4)
	}
	if empty := VectorSlice(v, n-1, 1); empty.Len != 0 {
		t.Errorf("a slice that drops every element has %d", empty.Len)
	}
}
