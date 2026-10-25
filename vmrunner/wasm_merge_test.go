package vmrunner

import (
	"bytes"
	"testing"
)

// Merging segments to make room for the image's leaves memory initialized
// to the same bytes, and joins the neighbours with the smallest gaps.
func TestMergeClosestSegments_KeepsMemoryAndJoinsTheSmallestGaps(t *testing.T) {
	segs := []dataSegment{
		{offset: 100, data: []byte{1, 2}},
		{offset: 0, data: []byte{9}},
		{offset: 103, data: []byte{3}},
		{offset: 200, data: []byte{4, 5}},
		{offset: 210, data: []byte{6}},
	}
	memory := func(segs []dataSegment) []byte {
		m := make([]byte, 256)
		for _, s := range segs {
			copy(m[s.offset:], s.data)
		}
		return m
	}
	merged := mergeClosestSegments(segs, 2)
	if len(merged) != 3 {
		t.Fatalf("%d segments after merging two pairs of 5, want 3: %v", len(merged), merged)
	}
	if !bytes.Equal(memory(merged), memory(segs)) {
		t.Fatal("merging changed the initialized memory")
	}
	if merged[1].offset != 100 || !bytes.Equal(merged[1].data, []byte{1, 2, 0, 3}) {
		t.Errorf("the 1-byte gap was not the one joined first: %+v", merged[1])
	}
	if merged[2].offset != 200 || !bytes.Equal(merged[2].data, []byte{4, 5, 0, 0, 0, 0, 0, 0, 0, 0, 6}) {
		t.Errorf("the 8-byte gap was not joined second: %+v", merged[2])
	}
}
