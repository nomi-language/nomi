package rt

import "testing"

func TestListCellSequenceRepeatsAndPreservesFrame(t *testing.T) {
	type node ListCell[int, node]
	xs := ConsCell[int, node](1, ConsCell[int, node](2, nil))
	fr := &Frame{}
	calls := 0
	src := SeqMap(ListCellSeq[int](xs), func(got *Frame, n int) int {
		if got != fr {
			t.Fatal("callback lost driving frame")
		}
		calls++
		return n * 10
	})
	if calls != 0 {
		t.Fatal("lazy map executed eagerly")
	}
	for i := 1; i <= 2; i++ {
		got := SeqToListCells[int, node](fr, src)
		if got.Head != 10 || got.Tail.Head != 20 || got.Len != 2 || calls != 2*i {
			t.Fatal("materialization changed order or cached traversal")
		}
	}
	if ListCellEachWhile[int](fr, xs, func(_ *Frame, n int) bool { return n != 1 }) {
		t.Fatal("ignored early stop")
	}
	if got := SeqToListCells[int, node](fr, ListCellSeq[int, node](nil)); got != nil {
		t.Fatal("empty materialization allocated a cell")
	}
	if xs.Head != 1 || xs.Tail.Head != 2 {
		t.Fatal("source mutated")
	}
}

func TestListEqualUsesComparatorForSharedCells(t *testing.T) {
	tail := Cons(2, Cons(3, nil))
	tests := []struct {
		name      string
		a, b      *List[int]
		wantCalls int
	}{
		{"same list", tail, tail, 1},
		{"shared tail", Cons(1, tail), Cons(1, tail), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			eq := func(a, b int) bool { calls++; return a == b && a != 2 }
			if ListEqual(tt.a, tt.b, eq) {
				t.Fatal("node identity bypassed element comparator")
			}
			if calls != tt.wantCalls {
				t.Fatalf("comparator calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
	if !ListEqual[int](nil, nil, func(int, int) bool { t.Fatal("empty list compared an element"); return false }) {
		t.Fatal("empty lists differ")
	}
}

func TestConsCellAllocationAndSharing(t *testing.T) {
	tail := Cons(2, Cons(3, nil))
	var result *List[int]
	if allocs := testing.AllocsPerRun(100, func() { result = Cons(1, tail) }); allocs != 1 {
		t.Fatalf("prepend allocations = %g, want one cell", allocs)
	}
	if result.Tail != tail || result.Len != 3 || tail.Len != 2 {
		t.Fatal("prepend did not preserve shared tail and cached lengths")
	}
}

func TestListCellOperationsPreserveEmptyAndSharing(t *testing.T) {
	type node ListCell[int, node]
	right := ConsCell[int, node](3, nil)
	left := ConsCell[int, node](1, ConsCell[int, node](2, nil))
	joined := ListCellConcat[int, node](left, right)
	if joined == left || joined.Tail.Tail != right || joined.Len != 3 || left.Len != 2 || left.Tail.Tail != nil {
		t.Fatal("concat must copy the left spine and share the right spine")
	}
	if ListCellConcat[int, node](nil, right) != right {
		t.Fatal("empty prefix lost sharing")
	}
	if got := ListCellHead[int, node](left); got.Tag != TagSome || got.Some != 1 {
		t.Fatal(got)
	}
	if got := ListCellHead[int, node](nil); got.Tag != TagNone {
		t.Fatal(got)
	}
	if got := ListCellTail[int, node](right); got.Tag != TagSome || got.Some != nil {
		t.Fatal("singleton tail must be Some(empty)")
	}
	if got := ListCellTail[int, node](nil); got.Tag != TagNone {
		t.Fatal("empty tail must be None")
	}
}

func TestListCellCountUsesStoredLength(t *testing.T) {
	type node ListCell[int, node]
	if got := ListCellCount[int, node](nil); got != 0 {
		t.Fatalf("empty count = %d", got)
	}
	tail := ConsCell[int, node](2, nil)
	xs := ConsCell[int, node](1, tail)
	if ListCellCount[int](xs) != 2 || ListCellCount[int](tail) != 1 {
		t.Fatal("persistent counts changed")
	}
	// A deliberately cyclic test cell distinguishes the cached protocol from
	// traversal: count must neither inspect the tail nor call an iterator.
	xs.Tail = xs
	if got := ListCellCount[int](xs); got != 2 {
		t.Fatalf("stored count = %d", got)
	}
}

func TestSortedCellsPreserveStabilityAndFrame(t *testing.T) {
	type item struct{ key, id int }
	type node ListCell[item, node]
	fr := &Frame{}
	values := []item{{2, 1}, {1, 2}, {2, 3}, {1, 4}}
	calls := 0
	src := Seq[item]{Run: func(got *Frame, yield func(*Frame, item) bool) bool {
		if got != fr {
			t.Fatal("source frame changed")
		}
		for _, v := range values {
			if !yield(got, v) {
				return false
			}
		}
		return true
	}}
	out := SeqSortWithCells[item, node](fr, src, func(got *Frame, a, b item) Ordering {
		if got != fr {
			t.Fatal("comparator frame changed")
		}
		calls++
		if a.key < b.key {
			return Ordering{Tag: TagLess}
		}
		if a.key > b.key {
			return Ordering{Tag: TagGreater}
		}
		return Ordering{Tag: TagEqual}
	})
	if calls == 0 || out.Len != 4 {
		t.Fatal("sort did not run")
	}
	for _, id := range []int{2, 4, 1, 3} {
		if out == nil || out.Head.id != id {
			t.Fatalf("unstable result at id %d", id)
		}
		out = out.Tail
	}
	if out != nil || values[0].id != 1 {
		t.Fatal("sort mutated source or added cells")
	}
	empty := Seq[item]{Run: func(*Frame, func(*Frame, item) bool) bool { return true }}
	if got := SeqSortWithCells[item, node](fr, empty, func(*Frame, item, item) Ordering { t.Fatal("compared empty input"); return Ordering{} }); got != nil {
		t.Fatal("empty sort allocated a cell")
	}
}
