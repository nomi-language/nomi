package vm

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// `Iter.to_vector` over a List view builds the vector through
// rt.SeqToVector, whose TestSeqToVector_PreallocatesFromKnownCount holds that
// the list's known_count sizes it.
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
	if items := rt.VectorToSlice(v); len(items) != 3 || items[0] != int64(1) || items[2] != int64(3) {
		t.Fatalf("to_vector = %v, want [1 2 3]", items)
	}
	empty, err := m.call(direct, []any{(*list)(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if ev, ok := empty.(rt.Vector[any]); !ok || ev.Len() != 0 {
		t.Fatalf("to_vector over [] = %#v, want the empty Vector", empty)
	}
}
