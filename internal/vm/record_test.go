package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestVMRecord_DebugKeepsNominalChildrenUnsupported(t *testing.T) {
	at := ir.At("record.nomi", 1, 1)
	f := ir.NewFunc(at, "render")
	arg := f.AddParam(ir.NewSymbol("record"), ir.ValStruct)
	b := f.NewBlock(at, "entry")
	render := ir.NewRenderDebug(at, f.NewTemp(), arg)
	b.Append(render)
	b.SetTerm(ir.NewReturn(at, render.Dst()))
	mod := ir.NewModule("record.nomi")
	mod.AddFunc(f)
	_, err := vm.New(mod, io.Discard).Run("render", rtStruct("", "point", rtStruct("Point")))
	if err == nil || !strings.Contains(err.Error(), "Debug on an unrepresented receiver (Point)") {
		t.Fatalf("unsupported record child: %v", err)
	}
}
