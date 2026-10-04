package irbuild

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"io"
	"testing"
)

func TestVMShaper_ListDemandsProduceExecutableArguments(t *testing.T) {
	at := ir.At("probe.nomi", 1, 1)
	for _, mode := range []string{"element", "nested", "length"} {
		f := ir.NewFunc(at, mode)
		arg := f.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
		b := f.NewBlock(at, "entry")
		dst := f.NewTemp()
		if mode == "length" {
			b.Append(ir.NewMatchListMinInto(at, dst, arg, 2))
		} else {
			b.Append(ir.NewProjElem(at, dst, arg, 1, ir.ValUnknown))
			if mode == "nested" {
				inner := f.NewTemp()
				b.Append(ir.NewProjElem(at, inner, dst, 0, ir.ValInt))
				dst = inner
			}
		}
		b.SetTerm(ir.NewReturn(at, dst))
		mod := ir.NewModule("probe.nomi")
		mod.AddFunc(f)
		args := vmArgShapes(mod, f)[0]
		xs, ok := args[0].(*rt.List[any])
		if !ok || xs == nil || xs.Len != 2 {
			t.Fatalf("%s input = %#v", mode, args[0])
		}
		got, err := vmRunV(vm.New(mod, io.Discard), mode, args...)
		want := "1"
		if mode == "length" {
			want = "True"
		}
		if err != nil || vmDisplay(got) != want {
			t.Fatalf("%s result = %v, %v", mode, got, err)
		}
	}
}

func TestVMShaper_ListComparisonDemandsExecutableArguments(t *testing.T) {
	at := ir.At("probe.nomi", 1, 1)
	for _, right := range []bool{false, true} {
		f := ir.NewFunc(at, "equal")
		arg := f.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
		b := f.NewBlock(at, "entry")
		empty, dst := f.NewTemp(), f.NewTemp()
		b.Append(ir.NewEmptyList(at, empty, ir.NewSymbol("Int")))
		lhs, rhs := arg, empty
		if right {
			lhs, rhs = rhs, lhs
		}
		b.Append(ir.NewCompare(at, dst, ir.OpEq, ir.ValContainer, lhs, rhs))
		b.SetTerm(ir.NewReturn(at, dst))
		mod := ir.NewModule("probe.nomi")
		mod.AddFunc(f)
		args := vmArgShapes(mod, f)[0]
		got, err := vmRunV(vm.New(mod, io.Discard), "equal", args...)
		if err != nil || vmDisplay(got) != "True" {
			t.Fatalf("right=%v: %v, %v", right, got, err)
		}
	}
}

func TestVMShaper_ListIterationDemandsExecutableArguments(t *testing.T) {
	at := ir.At("probe.nomi", 1, 1)
	for _, count := range []bool{false, true} {
		f := ir.NewFunc(at, "terminal")
		arg := f.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
		b := f.NewBlock(at, "entry")
		dst := f.NewTemp()
		if count {
			b.Append(ir.NewIter(at, dst, ir.IterCount, ir.IterOverList, arg))
		} else {
			seq := f.NewTemp()
			b.Append(ir.NewIter(at, seq, ir.IterView, ir.IterOverList, arg))
			b.Append(ir.NewIter(at, dst, ir.IterToList, ir.IterOverSeq, seq))
		}
		b.SetTerm(ir.NewReturn(at, dst))
		mod := ir.NewModule("probe.nomi")
		mod.AddFunc(f)
		args := vmArgShapes(mod, f)[0]
		if _, ok := args[0].(*rt.List[any]); !ok {
			t.Fatalf("input = %T", args[0])
		}
		got, err := vmRunV(vm.New(mod, io.Discard), "terminal", args...)
		want := "[]"
		if count {
			want = "0"
		}
		if err != nil || vmDisplay(got) != want {
			t.Fatalf("count=%v: %v, %v", count, got, err)
		}
	}
}
