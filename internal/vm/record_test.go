package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestVMRecord_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "structs-enums-distinct.md", `dbg point.x + point.y`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("record Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatalf("record Tour output differs: %v", diffs)
	}
}

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
