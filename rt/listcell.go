package rt

import "strings"

// ListCell is the shared storage layout for immutable cons lists. N is the
// recursive node type, so boxed and typed lists retain their own typed tails.
// A nil node is empty; Len caches the number of cells from this node onward.
type ListCell[T, N any] struct {
	Head T
	Tail *N
	Len  int
}

// ListCellShape admits named recursive list types with the shared cell layout.
type ListCellShape[T, N any] interface {
	~struct {
		Head T
		Tail *N
		Len  int
	}
}

// ConsCell prepends one cell, sharing tail in O(1).
func ConsCell[T any, N ListCellShape[T, N]](head T, tail *N) *N {
	n := 1
	if tail != nil {
		n += ListCell[T, N](*tail).Len
	}
	out := N(ListCell[T, N]{Head: head, Tail: tail, Len: n})
	return &out
}

// ListCellsEqual compares elements even when two operands share cells. The
// element comparator determines equality; node identity cannot replace it.
func ListCellsEqual[T any, N ListCellShape[T, N]](a, b *N, eq func(T, T) bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ListCell[T, N](*a).Len != ListCell[T, N](*b).Len {
		return false
	}
	for a != nil && b != nil {
		ac, bc := ListCell[T, N](*a), ListCell[T, N](*b)
		if !eq(ac.Head, bc.Head) {
			return false
		}
		a, b = ac.Tail, bc.Tail
	}
	return a == nil && b == nil
}

// HashListCells folds element hashes in list order.
func HashListCells[T any, N ListCellShape[T, N]](xs *N, hash func(T) uint64) uint64 {
	h := uint64(0x100)
	for xs != nil {
		cell := ListCell[T, N](*xs)
		h = HashMix(h, hash(cell.Head))
		xs = cell.Tail
	}
	return h
}

// FormatListCells renders a list using the supplied element renderer.
func FormatListCells[T any, N ListCellShape[T, N]](xs *N, render func(T) string) string {
	var b strings.Builder
	b.WriteByte('[')
	first := true
	for xs != nil {
		cell := ListCell[T, N](*xs)
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(render(cell.Head))
		xs = cell.Tail
	}
	b.WriteByte(']')
	return b.String()
}
