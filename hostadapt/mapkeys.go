package hostadapt

import (
	"cmp"
	"slices"
)

// MapEntry is one entry of a Go map, as SortedEntries answers it.
type MapEntry[K comparable, V any] struct {
	Key K
	Val V
}

// SortedEntries answers a Go map's entries in ascending key order. A Nomi
// Map keeps its insertion order, so an adapter that builds one from a Go map
// inserts in this order; Go's iteration order is random and would otherwise
// reach the Nomi value. NaN keys sort first, as cmp.Compare orders them.
// Entries are collected rather than keys, so a NaN key keeps its value.
func SortedEntries[M ~map[K]V, K cmp.Ordered, V any](m M) []MapEntry[K, V] {
	return SortedEntriesFunc(m, cmp.Compare[K])
}

// SortedEntriesFunc is SortedEntries for a key type that is not cmp.Ordered,
// ordered by compare (a struct key's generated field-by-field comparison).
func SortedEntriesFunc[M ~map[K]V, K comparable, V any](m M, compare func(K, K) int) []MapEntry[K, V] {
	es := make([]MapEntry[K, V], 0, len(m))
	for k, v := range m {
		es = append(es, MapEntry[K, V]{Key: k, Val: v})
	}
	slices.SortFunc(es, func(a, b MapEntry[K, V]) int { return compare(a.Key, b.Key) })
	return es
}

// CompareBool orders false before true.
func CompareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}
