package rt

import "strings"

// A Nomi `Set<T>` is std's `struct Set<T> { items: Map<T, Bool> }`: a map of
// its members to a presence value. The VM holds one as a record over that
// map, so the operations here are the map-shaped ones a set needs.

// MapKeysEachWhile pushes a map's keys in insertion order. A Set is a map of
// its members to a presence value, whatever that value's representation.
func MapKeysEachWhile[K, V any](fr *Frame, m Map[K, V], yield func(fr *Frame, item K) bool) bool {
	for _, e := range MapEntries(m) {
		if !yield(fr, e.Key) {
			return false
		}
	}
	return true
}

// MapKeysSeq views a map's keys as a push sequence.
func MapKeysSeq[K, V any](m Map[K, V]) Seq[K] {
	return Seq[K]{Run: func(fr *Frame, yield func(fr *Frame, item K) bool) bool {
		return MapKeysEachWhile(fr, m, yield)
	}}
}

// FormatSetElems renders ordered elements of either native or boxed sets as
// the set literal that builds them: `#{1, 2}`.
func FormatSetElems[T any](elems []T, render func(T) string) string {
	parts := make([]string, len(elems))
	for i, e := range elems {
		parts[i] = render(e)
	}
	return "#{" + strings.Join(parts, ", ") + "}"
}
