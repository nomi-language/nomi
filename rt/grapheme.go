package rt

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// The grapheme-cluster half of std/strings, and the reason this module has a
// dependency at all.
//
// # What a cluster is, and why nothing in the standard library answers it
//
// `String.length`, `String.slice` and `String.reverse` are defined over GRAPHEME
// CLUSTERS per UAX #29 — user-perceived characters — not bytes and not code
// points. std/strings.nomi states it as the definition rather than as a note
// (`String.length("héllo") == 5`, where the string is `e` plus U+0301). The three
// counts genuinely differ: a ZWJ family emoji is 11 bytes, 3 code points and 1
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
// the assertion that the BOUND symbol counts clusters, and
// tests/04-scalars-and-text/graphemes/ is the end-to-end fixture.

// StringLength is `String.length`: the number of grapheme clusters in s.
//
// Counted directly rather than by walking the clusters out, which is what
// std/strings means by calling it the fast path beside the generic `Iter.count`
// fold: no cluster is ever materialized, so this allocates nothing.
func StringLength(s string) int64 { return int64(uniseg.GraphemeClusterCount(s)) }

// StringSlice is `String.slice`: the clusters of s from start (inclusive) to end
// (exclusive), as a string.
//
// Out-of-range indices CLAMP rather than fault, and an inverted or empty range
// is "". std/strings declares no error case, and a Nomi program can observe
// the difference between "" and a trap.
func StringSlice(s string, start, end int64) string {
	clusters := graphemeClusters(s)
	lo, hi := int(start), int(end)
	if lo < 0 {
		lo = 0
	}
	if hi > len(clusters) {
		hi = len(clusters)
	}
	if lo > hi {
		return ""
	}
	return strings.Join(clusters[lo:hi], "")
}

// StringReverse is `String.reverse`: s with its clusters in the opposite order.
//
// By CLUSTER, so a combining mark stays attached to the base it modifies and a
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
// allocation is the header slice. Sized by CODE POINT count rather than by byte
// length: a cluster is at least one code point, so that is an exact upper bound,
// and it is a 4x tighter one than len(s) for non-Latin text. Counting runes is a
// scan with no property-table lookups, unlike asking uniseg for the exact
// cluster count, which would do the boundary work twice.
//
// The BOUNDARY RULE itself is EachGraphemeCluster's (seqsrc.go), which
// `impl Iter for String` also drives. This function is that walk collected,
// which is the whole difference between the two: `String.slice` needs every
// cluster at once and a pipeline needs them one at a time. Written over it
// rather than beside it because a second copy of a UAX #29 walk is the
// two-agreeing-copies shape this package keeps deleting — and here the copies
// would be forty lines apart in sibling files, which is the distance at which
// nobody notices one of them changing.
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
