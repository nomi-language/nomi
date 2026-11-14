package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// inPlaceFunc builds `p = make struct P x: 1; d = make update x: p, 2` in
// a block whose fault edge, when handlerReads, leads to a block that returns
// p, and answers the function and the update.
func inPlaceFunc(handlerReads bool) (*ir.Func, *ir.Make) {
	pos := ir.At("u.nomi", 1, 1)
	f := ir.NewFunc(pos, "f")
	entry := f.NewBlock(pos, "entry")
	handler := f.NewBlock(pos, "handler")
	one, two := f.NewTemp(), f.NewTemp()
	entry.Append(ir.NewInt(pos, one, 1))
	entry.Append(ir.NewInt(pos, two, 2))
	p := f.NewTemp()
	entry.Append(ir.NewMakeStruct(pos, p, ir.NewSymbol("P"), []string{"x"}, []ir.Temp{one}))
	d := f.NewTemp()
	up := ir.NewMakeUpdate(pos, d, p, []string{"x"}, []ir.Temp{two})
	entry.Append(up)
	entry.SetTerm(ir.NewReturn(pos, d))
	if handlerReads {
		entry.SetFault(pos, handler.ID())
		handler.SetTerm(ir.NewReturn(pos, p))
	} else {
		handler.SetTerm(ir.NewReturnUnit(pos))
	}
	return f, up
}

// TestInPlaceUpdates_AFaultEdgeKeepsTheBaseLive: a handler that reads the
// base keeps it live through the block that faults to it, so the update
// copies; without the edge it writes in place.
func TestInPlaceUpdates_AFaultEdgeKeepsTheBaseLive(t *testing.T) {
	f, up := inPlaceFunc(false)
	if !ir.InPlaceUpdates(f)[up] {
		t.Fatalf("the update is the base's last use, and it is not eligible")
	}
	f, up = inPlaceFunc(true)
	if ir.InPlaceUpdates(f)[up] {
		t.Fatalf("the fault handler reads the base, and the update is eligible")
	}
}

// TestInPlaceUpdates_NoneIsNil: a function with no update answers nil, so a
// consumer pays one walk for it.
func TestInPlaceUpdates_NoneIsNil(t *testing.T) {
	pos := ir.At("u.nomi", 1, 1)
	f := ir.NewFunc(pos, "f")
	b := f.NewBlock(pos, "entry")
	b.SetTerm(ir.NewReturnUnit(pos))
	if got := ir.InPlaceUpdates(f); got != nil {
		t.Fatalf("InPlaceUpdates = %v, want nil", got)
	}
}
