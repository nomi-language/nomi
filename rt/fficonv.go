package rt

// ListFromGo builds a Nomi List from a Go slice, converting each element.
//
// It takes an element conversion even when the element needs none —
// `ListFromGo(xs, func(v string) string { return v })` — so there is one
// function rather than a fast path beside a mapping one that would have to
// agree about the element rule forever. Go inlines the identity literal.
//
// Built from the tail forward, because a cons list is cheap to prepend to and
// `Cons` caches the length as it goes — so this is one pass and n allocations,
// with no reversal.
func ListFromGo[G, T any](xs []G, elem func(G) T) *List[T] {
	var out *List[T]
	for i := len(xs) - 1; i >= 0; i-- {
		out = Cons(elem(xs[i]), out)
	}
	return out
}
