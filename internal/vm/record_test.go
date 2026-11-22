package vm_test

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A named struct inside a record renders as the literal that builds it, as
// rt.DebugText renders it at the top.
func TestVMRecord_DebugRendersNominalChildren(t *testing.T) {
	at := ir.At("record.nomi", 1, 1)
	f := ir.NewFunc(at, "render")
	arg := f.AddParam(ir.NewSymbol("record"), ir.ValStruct)
	b := f.NewBlock(at, "entry")
	render := ir.NewRenderDebug(at, f.NewTemp(), arg)
	b.Append(render)
	b.SetTerm(ir.NewReturn(at, render.Dst()))
	mod := ir.NewModule("record.nomi")
	mod.AddFunc(f)
	got, err := vm.New(mod, io.Discard).Run("render", rtStruct("", "point", rtStruct("Point")))
	if err != nil || got != any("{point: Point{}}") {
		t.Fatalf("record child: %v, %v", got, err)
	}
}
