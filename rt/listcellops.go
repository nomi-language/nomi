package rt

import "sort"

// ListCellEachWhile traverses shared cons storage without copying the source.
func ListCellEachWhile[T any, N ListCellShape[T, N]](fr *Frame, xs *N, yield func(*Frame, T) bool) bool {
	for node := xs; node != nil; {
		cell := ListCell[T, N](*node)
		if !yield(fr, cell.Head) {
			return false
		}
		node = cell.Tail
	}
	return true
}

// ListCellSeq is a repeatable lazy view of immutable cons storage.
func ListCellSeq[T any, N ListCellShape[T, N]](xs *N) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(*Frame, T) bool) bool {
		return ListCellEachWhile(fr, xs, yield)
	}}
}

// SeqToListCells materializes in source order using the consumer's cell type.
func SeqToListCells[T any, N ListCellShape[T, N]](fr *Frame, src Seq[T]) *N {
	var items []T
	src.Run(fr, func(_ *Frame, item T) bool {
		items = append(items, item)
		return true
	})
	var out *N
	for i := len(items) - 1; i >= 0; i-- {
		out = ConsCell[T, N](items[i], out)
	}
	return out
}

// ListCellHead reads the first element without copying cells.
func ListCellHead[T any, N ListCellShape[T, N]](xs *N) Maybe[T] {
	if xs == nil {
		return None[T]()
	}
	return Some(ListCell[T, N](*xs).Head)
}

// ListCellTail distinguishes an empty input from a singleton's empty tail.
func ListCellTail[T any, N ListCellShape[T, N]](xs *N) Maybe[*N] {
	if xs == nil {
		return None[*N]()
	}
	return Some(ListCell[T, N](*xs).Tail)
}

// ListCellConcat rebuilds only the left operand and shares the right operand.
func ListCellConcat[T any, N ListCellShape[T, N]](a, b *N) *N {
	if a == nil {
		return b
	}
	cells := make([]T, 0, ListCell[T, N](*a).Len)
	for node := a; node != nil; {
		cell := ListCell[T, N](*node)
		cells = append(cells, cell.Head)
		node = cell.Tail
	}
	out := b
	for i := len(cells) - 1; i >= 0; i-- {
		out = ConsCell[T, N](cells[i], out)
	}
	return out
}

// ListCellCount reads the cached length without traversing the list.
func ListCellCount[T any, N ListCellShape[T, N]](xs *N) int64 {
	if xs == nil {
		return 0
	}
	return int64(ListCell[T, N](*xs).Len)
}

// SortSliceToListCells stably sorts an owned scratch slice and constructs a
// persistent list. Callers must not pass storage exposed by a language value.
func SortSliceToListCells[T any, N ListCellShape[T, N]](items []T, less func(T, T) bool) *N {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	var out *N
	for i := len(items) - 1; i >= 0; i-- {
		out = ConsCell[T, N](items[i], out)
	}
	return out
}
