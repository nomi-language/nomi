package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestVMVariant_IdentityPayloadAndArity(t *testing.T) {
	for _, arity := range []int{0, 1, 2} {
		at := ir.At("variant.nomi", 1, 1)
		f := ir.NewFunc(at, "make")
		b := f.NewBlock(at, "entry")
		var args []ir.Temp
		for i := 0; i < arity; i++ {
			n := ir.NewInt(at, f.NewTemp(), int64(42+i))
			b.Append(n)
			args = append(args, n.Dst())
		}
		n := ir.NewMakeVariant(at, f.NewTemp(), ir.NewSymbol("package.Signal"), "Value", args)
		b.Append(n)
		b.SetTerm(ir.NewReturn(at, n.Dst()))
		mod := ir.NewModule("variant.nomi")
		mod.AddFunc(f)
		got, err := vm.New(mod, io.Discard).Run("make")
		if arity == 2 {
			if err == nil || !strings.Contains(err.Error(), "only bare or single-payload variants") {
				t.Fatalf("multi-payload construction: %v, %v", got, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		variant, ok := got.(*rt.Record)
		if !ok || variant.Desc.Kind != rt.KindEnum || variant.Desc.Name != "package.Signal" || variant.Variant().Name != "Value" {
			t.Fatalf("lost enum identity: %#v", got)
		}
		if arity == 0 && variant.NumFields() != 0 {
			t.Fatalf("bare variant carries %d fields", variant.NumFields())
		}
		if arity == 1 && (variant.NumFields() != 1 || variant.Field(0) != any(int64(42))) {
			t.Fatalf("payload = %s", rt.RowText(variant))
		}
	}
}

func TestVMVariant_MatchChecksEnumAndVariant(t *testing.T) {
	at := ir.At("variant.nomi", 1, 1)
	f := ir.NewFunc(at, "matches")
	arg := f.AddParam(ir.NewSymbol("arg"), ir.ValVariant)
	b := f.NewBlock(at, "entry")
	m := ir.NewMatchVariantInto(at, f.NewTemp(), arg, ir.NewSymbol("signals.Signal"), "Value")
	b.Append(m)
	b.SetTerm(ir.NewReturn(at, m.Dst()))
	mod := ir.NewModule("variant.nomi")
	mod.AddFunc(f)
	machine := vm.New(mod, io.Discard)
	for _, tc := range []struct{ enum, variant, want string }{
		{"signals.Signal", "Value", "True"},
		{"signals.Signal", "Idle", "False"},
		{"other.Signal", "Value", "False"},
	} {
		got, err := machine.Run("matches", rtVariant(tc.enum, tc.variant))
		if err != nil || rt.DisplayText(got) != tc.want {
			t.Fatalf("%s.%s: %v, %v; want %s", tc.enum, tc.variant, got, err, tc.want)
		}
	}
	if _, err := machine.Run("matches", int64(1)); err == nil {
		t.Fatal("variant test accepted a non-variant subject")
	}
}
