package rt

import "testing"

// vecAllocSink keeps each result reachable, so the compiler cannot drop an
// allocation the operation makes.
var vecAllocSink Vector[int64]

// TestVectorAllocations pins how many heap objects push, set and VectorOf
// make. A vector of up to 32 elements lives in its tail, and the tail shares
// one object with the trie header (vecAllocTail), so each update on it is one
// allocation. On a trie, set allocates the header, the copied leaf (its
// elements inline) and each copied branch: one object for a full branch, two
// for a partial one (the root here), whose slots are a separate slice.
func TestVectorAllocations(t *testing.T) {
	small := VectorOf([]int64{1, 2, 3, 4, 5, 6})
	full := VectorOf(make([]int64, 32))
	// 1<<16 elements: 2047 leaves under branches at shifts 5 and 10 (full)
	// and a root at shift 15 with 2 children, and a full tail of 32.
	large := VectorOf(make([]int64, 1<<16))
	largeOpen := VectorPush(large, 0) // a tail of 1

	cases := []struct {
		name string
		op   func()
		want float64
	}{
		{"VectorOf, 6", func() { vecAllocSink = VectorOf([]int64{1, 2, 3, 4, 5, 6}) }, 1},
		{"set, 6", func() { vecAllocSink = VectorSet(small, 3, 9) }, 1},
		{"push, 6", func() { vecAllocSink = VectorPush(small, 9) }, 1},
		{"set, 32", func() { vecAllocSink = VectorSet(full, 31, 9) }, 1},
		// The full tail becomes a leaf node (1); the root is a fresh branch
		// of one child (2); x starts a new tail (1).
		{"push, 32", func() { vecAllocSink = VectorPush(full, 9) }, 4},
		{"set in the tail, 65536", func() { vecAllocSink = VectorSet(large, 65535, 9) }, 1},
		// Header 1, leaf 1, two full branches 1 each, the root 2.
		{"set in the trie, 65536", func() { vecAllocSink = VectorSet(large, 0, 9) }, 6},
		{"push onto a tail of 1, 65537", func() { vecAllocSink = VectorPush(largeOpen, 9) }, 1},
		// The full tail becomes a leaf node (1); the path copy is the root (2)
		// and two branches that are full after it (1 each); the new tail (1).
		{"push onto a full tail, 65536", func() { vecAllocSink = VectorPush(large, 9) }, 6},
	}
	for _, c := range cases {
		if got := testing.AllocsPerRun(100, c.op); got != c.want {
			t.Errorf("%s: %v allocations, want %v", c.name, got, c.want)
		}
	}
}
