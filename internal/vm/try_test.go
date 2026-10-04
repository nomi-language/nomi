package vm_test

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestVMTry_IdentityAndPropagation(t *testing.T) {
	at := ir.At("try.nomi", 1, 1)
	f := ir.NewFunc(at, "unwrap")
	arg := f.AddParam(ir.NewSymbol("operand"), ir.ValVariant)
	b := f.NewBlock(at, "entry")
	b.Append(ir.NewTry(at, arg, "try operand"))
	answer := ir.NewInt(at, f.NewTemp(), 42)
	b.Append(answer)
	b.SetTerm(ir.NewReturn(at, answer.Dst()))
	mod := ir.NewModule("try.nomi")
	mod.AddFunc(f)
	m := vm.New(mod, io.Discard)
	for _, tc := range []struct {
		enum, variant string
		unwind        bool
	}{
		{"maybe.Maybe", "Some", false}, {"maybe.Maybe", "None", true},
		{"results.Result", "Ok", false}, {"results.Result", "Err", true},
	} {
		v := rtVariant(tc.enum, tc.variant)
		if tc.variant != "None" {
			v = rtVariant(tc.enum, tc.variant, int64(7))
		}
		got, err := m.Run("unwrap", v)
		if err != nil {
			t.Fatal(err)
		}
		if tc.unwind {
			if got != any(v) {
				t.Fatalf("propagation lost operand: %#v", got)
			}
		} else if n, ok := got.(int64); !ok || n != 42 {
			t.Fatalf("success did not continue: %#v", got)
		}
	}
	for _, v := range []any{
		int64(1),
		rtVariant("other.Result", "Err"),
		rtVariant("literals.Fragment", "Dynamic"),
		rtVariant("maybe.Maybe", "Err"),
	} {
		if _, err := m.Run("unwrap", v); err == nil {
			t.Fatalf("accepted invalid try operand %#v", v)
		}
	}
}
