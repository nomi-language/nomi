package vm_test

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestVMListPattern_LengthAndSuffixBounds(t *testing.T) {
	at := ir.At("list.nomi", 1, 1)
	mod := ir.NewModule("list.nomi")
	for _, name := range []string{"empty", "pair", "nonempty", "head", "tail", "suffix0", "suffix3"} {
		f := ir.NewFunc(at, name)
		arg := f.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
		b := f.NewBlock(at, "entry")
		dst := f.NewTemp()
		switch name {
		case "empty":
			b.Append(ir.NewMatchListLenInto(at, dst, arg, 0))
		case "pair":
			b.Append(ir.NewMatchListLenInto(at, dst, arg, 2))
		case "nonempty":
			b.Append(ir.NewMatchListMinInto(at, dst, arg, 1))
		case "head":
			b.Append(ir.NewProjElem(at, dst, arg, 0, ir.ValInt))
		case "tail":
			b.Append(ir.NewProjSuffix(at, dst, arg, 1, ir.ValContainer))
		case "suffix0":
			b.Append(ir.NewProjSuffix(at, dst, arg, 0, ir.ValContainer))
		case "suffix3":
			b.Append(ir.NewProjSuffix(at, dst, arg, 3, ir.ValContainer))
		}
		b.SetTerm(ir.NewReturn(at, dst))
		mod.AddFunc(f)
	}
	machine := vm.New(mod, io.Discard)
	tail := rtList(int64(2))
	xs := rt.ConsCell[any, rt.List[any]](int64(1), tail)
	var empty *rt.List[any]
	for _, tc := range []struct {
		name  string
		input *rt.List[any]
		want  string
	}{
		{"empty", empty, "True"}, {"empty", xs, "False"},
		{"pair", xs, "True"}, {"pair", tail, "False"},
		{"nonempty", empty, "False"}, {"nonempty", tail, "True"},
		{"head", xs, "1"},
	} {
		got, err := machine.Run(tc.name, tc.input)
		if err != nil || rt.DisplayText(got) != tc.want {
			t.Fatalf("%s: %v, %v", tc.name, got, err)
		}
	}
	if got, err := machine.Run("tail", xs); err != nil || got != any(tail) {
		t.Fatalf("suffix lost sharing: %v, %v", got, err)
	}
	if got, err := machine.Run("tail", tail); err != nil || got != any(empty) {
		t.Fatalf("singleton suffix: %v, %v", got, err)
	}
	if got, err := machine.Run("suffix0", empty); err != nil || got != any(empty) {
		t.Fatalf("zero suffix: %v, %v", got, err)
	}
	for _, name := range []string{"empty", "head", "tail"} {
		if _, err := machine.Run(name, int64(1)); err == nil {
			t.Fatalf("%s accepted wrong subject", name)
		}
	}
	for _, name := range []string{"head", "tail", "suffix3"} {
		if _, err := machine.Run(name, empty); err == nil {
			t.Fatalf("%s accepted out-of-bounds projection", name)
		}
	}
}
