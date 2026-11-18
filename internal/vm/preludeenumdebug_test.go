package vm_test

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestVMPreludeEnumDebug_RejectsUnsupportedIdentityAndPayload(t *testing.T) {
	at := ir.At("enum-debug.nomi", 1, 1)
	f := ir.NewFunc(at, "render")
	arg := f.AddParam(ir.NewSymbol("value"), ir.ValVariant)
	b := f.NewBlock(at, "entry")
	r := ir.NewRenderDebug(at, f.NewTemp(), arg)
	b.Append(r)
	b.SetTerm(ir.NewReturn(at, r.Dst()))
	mod := ir.NewModule("enum-debug.nomi")
	mod.AddFunc(f)
	for _, v := range []*rt.Record{
		rtVariant("other.Result", "Ok", int64(1)),
		rtVariant("results.Result", "Unknown"),
		rtVariant("maybe.Maybe", "Some"),
		rtVariant("maybe.Maybe", "None", int64(1)),
		rtVariant("maybe.Maybe", "Some", rtStruct("Point")),
	} {
		if got, err := vm.New(mod, io.Discard).Run("render", v); err == nil {
			t.Fatalf("rendered unsupported %#v as %#v", v, got)
		}
	}
}
