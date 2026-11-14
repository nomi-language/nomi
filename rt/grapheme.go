package rt

import (
	"math/bits"
	"strings"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"

	"github.com/rivo/uniseg"
)

// The grapheme-cluster half of std/strings, and the reason this module has a
// dependency at all.
//
// # What a cluster is, and why nothing in the standard library answers it
//
// `String.length`, `String.slice` and `String.reverse` are defined over grapheme
// clusters per UAX #29 — user-perceived characters — not bytes and not code
// points. std/strings.nomi states it as the definition rather than as a note
// (`String.length("héllo") == 5`, where the string is `e` plus U+0301). The three
// counts differ: a ZWJ family emoji is 11 bytes, 3 code points and 1
// cluster. So there is no implementation of any of these over `unicode/utf8`,
// only a wrong one, and `unicode/utf8` is the most Go's standard library has.
// Cluster boundaries need the UAX #29 property tables, which is what
// github.com/rivo/uniseg is.
//
// # One implementation
//
// Two implementations of a boundary rule that agree on the inputs a fixture
// names is a divergence waiting for the input it does not, so the cluster rules
// live here once. TestGraphemeCountIsNotACodepointCount in internal/irbuild is
// the assertion that the bound symbol counts clusters, and
// tests/04-scalars-and-text/graphemes/ is the end-to-end fixture.

// StringLength is `String.length`: the number of grapheme clusters in s.
//
// A string of graphemeIndexMin bytes or more answers from its cached index
// (see "Indexed access" below), so a loop that asks for the length on each
// turn walks the string once. A shorter one is counted directly: no cluster
// is materialized, so this allocates nothing.
func StringLength(s string) int64 {
	if len(s) >= graphemeIndexMin {
		return graphemesOf(s).count
	}
	if byteIndexed(s) {
		return int64(len(s))
	}
	graphemeWork(len(s))
	return int64(uniseg.GraphemeClusterCount(s))
}

// StringSlice is `String.slice`: the clusters of s from start (inclusive) to end
// (exclusive), as a string.
//
// Out-of-range indices clamp rather than fault, and an inverted or empty range
// is "". std/strings declares no error case, and a Nomi program can observe
// the difference between "" and a trap. A bound past int64's range clamps
// as one past the string does.
//
// `String.take` and `String.drop` are this function, so all three cost what
// two seeks into s's index cost (see "Indexed access").
func StringSlice(s string, start, end Int) string {
	lo, hi := start.Saturated(), end.Saturated()
	if lo < 0 {
		lo = 0
	}
	if lo >= hi {
		return ""
	}
	ix := graphemesOf(s)
	if lo >= ix.count {
		return ""
	}
	a, state := ix.seek(lo)
	var b int
	if hi-lo <= graphemeMarkEvery {
		b, _ = ix.walk(a, state, hi-lo)
	} else {
		b, _ = ix.seek(hi)
	}
	return s[a:b]
}

// StringReverse is `String.reverse`: s with its clusters in the opposite order.
//
// By cluster, so a combining mark stays attached to the base it modifies and a
// ZWJ emoji stays one emoji. Reversing code points or bytes instead produces
// mojibake for exactly the inputs this function exists to handle.
func StringReverse(s string) string {
	clusters := graphemeClusters(s)
	for i, j := 0, len(clusters)-1; i < j; i, j = i+1, j-1 {
		clusters[i], clusters[j] = clusters[j], clusters[i]
	}
	return strings.Join(clusters, "")
}

// graphemeClusters splits s into its grapheme clusters. The empty string yields
// an empty slice, so a caller's length is the cluster count with no zero case.
//
// The clusters are substrings of s and share its backing array; the only
// allocation is the header slice. Sized by code point count rather than by byte
// length: a cluster is at least one code point, so that is an exact upper bound,
// and it is a 4x tighter one than len(s) for non-Latin text. Counting runes is a
// scan with no property-table lookups, unlike asking uniseg for the exact
// cluster count, which would do the boundary work twice.
//
// The boundary rule itself is EachGraphemeCluster's (seqsrc.go), which
// `impl Iter for String` also drives. This function is that walk collected,
// and that is the only difference between the two: `String.slice` needs every
// cluster at once and a pipeline needs them one at a time. Written over it
// rather than beside it because a second copy of a UAX #29 walk is the
// two-agreeing-copies shape this package avoids, and copies in sibling files
// can drift without anyone noticing.
func graphemeClusters(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, utf8.RuneCountInString(s))
	EachGraphemeCluster(s, func(cluster string) bool {
		out = append(out, cluster)
		return true
	})
	return out
}

// # Indexed access
//
// `String.slice`, `take`, `drop` and `length` name clusters by index, and a
// cluster's byte offset is known only by walking the boundary rule from the
// start. Doing that walk on every call makes reading a string a cluster at a
// time quadratic: 20,000 one-cluster slices of a 20,000-cluster string were
// 400 million segmenter steps. Stopping the walk at the slice's end would
// still be O(index) per call, so a left-to-right scan would stay quadratic.
//
// So the first indexed call on a string of graphemeIndexMin bytes or more
// walks it once and keeps a graphemeIndex: its cluster count, and the byte
// offset and segmenter state of every graphemeMarkEvery-th cluster. A seek
// starts at the nearest mark below it and walks at most graphemeMarkEvery-1
// clusters, so a slice costs a bounded number of steps whatever its index.
// The mark carries the segmenter state the full walk had there, so resuming
// from it gives exactly the clusters the walk from the start gives; the
// boundary rule is never restarted mid-string.
//
// A string with no byte at or above 0x80 and no '\r' needs no marks: every
// byte is one cluster, so the cluster index is the byte index. "\r\n" is one
// cluster (UAX #29 GB3), which is why '\r' leaves the fast path; every other
// ASCII pair breaks.
//
// A Nomi String is a Go string in an `any`, with no header of its own to hang
// the index on, so the indexes live in a small table keyed by the string's
// data pointer and length. An entry holds its string, which keeps the bytes at
// that address alive and so makes (pointer, length) name those bytes for as
// long as the entry exists: a substring sharing the pointer differs in length,
// and freed memory cannot be reused under a live entry. Entries are
// immutable and published with atomic stores, so concurrent tasks read them
// without locks; two tasks indexing the same string at once both build and
// one store wins, which costs time and never correctness. The table has
// graphemeIndexSlots slots, so at most that many strings are kept alive past
// their last use, until another string's index replaces them. A marks entry is
// two ints per graphemeMarkEvery clusters.
//
// A string under graphemeIndexMin bytes is indexed afresh on each call and not
// kept: its walk is bounded by the threshold, so a scan of it costs a constant
// per call either way.

const (
	graphemeIndexMin   = 256
	graphemeMarkEvery  = 32
	graphemeSlotBits   = 3
	graphemeIndexSlots = 1 << graphemeSlotBits
)

// graphemeIndex is what a seek into s needs. When byteIndexed, marks is nil
// and cluster i is byte i.
type graphemeIndex struct {
	s           string
	count       int64
	byteIndexed bool
	marks       []graphemeMark // marks[k] is cluster k*graphemeMarkEvery
}

type graphemeMark struct{ offset, state int }

var graphemeIndexes [graphemeIndexSlots]atomic.Pointer[graphemeIndex]

// graphemeWorkHook, when set, is told of every unit of work the indexed
// access does: one per segmenter step, one per byte the ASCII check reads.
// Only TestGraphemeScanIsLinear sets it, before any parallel test runs.
var graphemeWorkHook func(n int)

func graphemeWork(n int) {
	if graphemeWorkHook != nil {
		graphemeWorkHook(n)
	}
}

// byteIndexed reports whether every byte of s is one grapheme cluster.
func byteIndexed(s string) bool {
	graphemeWork(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= utf8.RuneSelf || c == '\r' {
			return false
		}
	}
	return true
}

// graphemesOf returns s's index: the kept one when s has one, else a new one,
// kept when s is long enough to be worth keeping.
func graphemesOf(s string) *graphemeIndex {
	if len(s) < graphemeIndexMin {
		return buildGraphemeIndex(s)
	}
	data := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	h := (data>>4 ^ uintptr(len(s))) * 0x9E3779B97F4A7C15
	slot := &graphemeIndexes[h>>(bits.UintSize-graphemeSlotBits)]
	if ix := slot.Load(); ix != nil && len(ix.s) == len(s) && unsafe.StringData(ix.s) == unsafe.StringData(s) {
		return ix
	}
	ix := buildGraphemeIndex(s)
	slot.Store(ix)
	return ix
}

func buildGraphemeIndex(s string) *graphemeIndex {
	if byteIndexed(s) {
		return &graphemeIndex{s: s, count: int64(len(s)), byteIndexed: true}
	}
	ix := &graphemeIndex{s: s}
	state, offset := -1, 0
	for offset < len(s) {
		if ix.count%graphemeMarkEvery == 0 {
			ix.marks = append(ix.marks, graphemeMark{offset, state})
		}
		var c string
		c, state = graphemeStep(s[offset:], state)
		offset += len(c)
		ix.count++
	}
	return ix
}

// seek returns the byte offset where cluster n starts (len(s) when n is the
// count or past it) and the segmenter state there.
func (ix *graphemeIndex) seek(n int64) (int, int) {
	if n >= ix.count {
		return len(ix.s), -1
	}
	if ix.byteIndexed {
		return int(n), -1
	}
	m := ix.marks[n/graphemeMarkEvery]
	return ix.walk(m.offset, m.state, n%graphemeMarkEvery)
}

// walk steps n clusters from offset, stopping early at the end of s.
func (ix *graphemeIndex) walk(offset, state int, n int64) (int, int) {
	if ix.byteIndexed {
		return min(offset+int(n), len(ix.s)), -1
	}
	for ; n > 0 && offset < len(ix.s); n-- {
		var c string
		c, state = graphemeStep(ix.s[offset:], state)
		offset += len(c)
	}
	return offset, state
}

// graphemeStep is the boundary rule: the first cluster of s, and the
// segmenter state after it. Every cluster walk in rt goes through it.
func graphemeStep(s string, state int) (string, int) {
	graphemeWork(1)
	c, _, _, state := uniseg.FirstGraphemeClusterInString(s, state)
	return c, state
}
