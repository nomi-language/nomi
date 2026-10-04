package vm_test

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"io"
	"testing"
)

func TestVMNot_BooleanValues(t *testing.T) {
	at := ir.At("not.nomi", 1, 1)
	f := ir.NewFunc(at, "negate")
	arg := f.AddParam(ir.NewSymbol("arg"), ir.ValBool)
	b := f.NewBlock(at, "entry")
	n := ir.NewNot(at, f.NewTemp(), arg)
	b.Append(n)
	b.SetTerm(ir.NewReturn(at, n.Dst()))
	mod := ir.NewModule("not.nomi")
	mod.AddFunc(f)
	machine := vm.New(mod, io.Discard)
	for _, b := range []bool{true, false} {
		got, err := machine.Run("negate", b)
		if err != nil || got != !b {
			t.Fatalf("negate %v: %v, %v", b, got, err)
		}
	}
	if _, err := machine.Run("negate", int64(1)); err == nil {
		t.Fatal("non-Bool operand accepted")
	}
}
