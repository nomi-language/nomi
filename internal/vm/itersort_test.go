package vm

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func TestIterSort_RejectsMalformedOperands(t *testing.T) {
	at := ir.At("sort.nomi", 1, 1)
	callback := ir.NewFunc(at, "bad_ordering")
	callback.AddParam(ir.NewSymbol("a"), ir.ValInt)
	callback.AddParam(ir.NewSymbol("b"), ir.ValInt)
	cb := callback.NewBlock(at, "entry")
	c := ir.NewInt(at, callback.NewTemp(), 1)
	cb.Append(c)
	cb.SetTerm(ir.NewReturn(at, c.Dst()))
	for _, tc := range []struct {
		op   ir.IterOp
		arg  any
		want string
	}{
		{ir.IterTake, "2", "Int count"},
		{ir.IterSortWith, int64(2), "binary comparator"},
		{ir.IterSortWith, &functionValue{body: callback, arity: 2}, "return Ordering"},
	} {
		f := ir.NewFunc(at, "terminal")
		src, arg, dst := f.NewTemp(), f.NewTemp(), f.NewTemp()
		n := ir.NewIter(at, dst, tc.op, ir.IterOverSeq, src, arg)
		fr := testFrame(f, &rt.Frame{})
		fr.write(src, rt.ListCellSeq[any, list](listOf([]any{int64(2), int64(1)})))
		fr.write(arg, tc.arg)
		if err := New(ir.NewModule("sort"), io.Discard).iterInstr(fr, n); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.op, err)
		}
	}
}
