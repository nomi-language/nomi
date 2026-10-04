package vm

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// `Iter.to_vector` over a List view sizes the vector from the list's
// known_count, so the backing slice is allocated once at exactly the length.
func TestIterToVector_SizesFromTheSourcesKnownCount(t *testing.T) {
	at := ir.At("to-vector.nomi", 1, 1)
	mod := ir.NewModule("to-vector.nomi")
	direct := ir.NewFunc(at, "direct")
	xs := direct.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
	b := direct.NewBlock(at, "entry")
	view := ir.NewIter(at, direct.NewTemp(), ir.IterView, ir.IterOverList, xs)
	out := ir.NewIter(at, direct.NewTemp(), ir.IterToVector, ir.IterOverSeq, view.Dst())
	b.Append(view)
	b.Append(out)
	b.SetTerm(ir.NewReturn(at, out.Dst()))
	mod.AddFunc(direct)
	m := New(mod, io.Discard)
	got, err := m.call(direct, []any{cons(int64(1), cons(int64(2), cons(int64(3), nil)))})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(rt.Vector[any])
	if !ok {
		t.Fatalf("to_vector answered %T, want rt.Vector[any]", got)
	}
	if v.Len != 3 || v.Items[0] != int64(1) || v.Items[2] != int64(3) {
		t.Fatalf("to_vector = %v, want [1 2 3]", v.Items[v.Start:v.Start+v.Len])
	}
	if cap(v.Items) != 3 {
		t.Fatalf("capacity %d, want 3: the list's known_count did not size the vector", cap(v.Items))
	}
	empty, err := m.call(direct, []any{(*list)(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if ev, ok := empty.(rt.Vector[any]); !ok || ev.Len != 0 {
		t.Fatalf("to_vector over [] = %#v, want the empty Vector", empty)
	}
}
