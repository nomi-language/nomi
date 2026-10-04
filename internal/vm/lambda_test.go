package vm_test

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestVMLambda_TourRecordedAnswer(t *testing.T) {
	ids := []string{"functions-and-lambdas.md:L51"}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", ids, vmPathResolver(t, "tour"))
	if len(got.Cases) != len(ids) || len(refused) != 0 {
		t.Fatalf("completed %d; refused: %v", len(got.Cases), refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}

func TestVMLambda_CapturesOutliveIndependentActivations(t *testing.T) {
	at := ir.At("closure.nomi", 1, 1)
	body := ir.NewFunc(at, "body")
	x := body.AddParam(ir.NewSymbol("x"), ir.ValInt)
	cap := body.AddParam(ir.NewSymbol("captured"), ir.ValInt)
	bb := body.NewBlock(at, "entry")
	// Return the captured operand so each invocation identifies its own environment.
	_ = x
	bb.SetTerm(ir.NewReturn(at, cap))
	maker := ir.NewFunc(at, "maker")
	arg := maker.AddParam(ir.NewSymbol("n"), ir.ValInt)
	mb := maker.NewBlock(at, "entry")
	closure := ir.NewFuncValue(at, maker.NewTemp(), body, arg)
	mb.Append(closure)
	mb.SetTerm(ir.NewReturn(at, closure.Dst()))
	invoke := ir.NewFunc(at, "invoke")
	fn := invoke.AddParam(ir.NewSymbol("fn"), ir.ValFunc)
	ib := invoke.NewBlock(at, "entry")
	zero := ir.NewInt(at, invoke.NewTemp(), 0)
	ib.Append(zero)
	call := ir.NewIndirectCall(at, invoke.NewTemp(), ir.OrdinaryCall, fn, zero.Dst())
	ib.Append(call)
	ib.SetTerm(ir.NewReturn(at, call.Dst()))
	mod := ir.NewModule("closure.nomi")
	mod.AddFunc(maker)
	mod.AddFunc(invoke)
	machine := vm.New(mod, io.Discard)
	a, err := machine.Run("maker", int64(3))
	if err != nil {
		t.Fatal(err)
	}
	b, err := machine.Run("maker", int64(7))
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range []any{a, b, a} {
		got, err := machine.Run("invoke", f)
		if err != nil || got != any([]int64{3, 7, 3}[i]) {
			t.Fatalf("capture %d: %v, %v", i, got, err)
		}
	}
	if _, err := machine.Run("invoke", int64(0)); err == nil {
		t.Fatal("accepted non-callable")
	}
}

func TestVMNamedFunctionValue_LinksByDeclaration(t *testing.T) {
	target, sameName := ir.NewSymbol("answer"), ir.NewSymbol("answer")
	entry := ir.NewFunc(linkPos, "main")
	b := entry.NewBlock(linkPos, "entry")
	ref := ir.NewRefFunc(linkPos, entry.NewTemp(), target)
	b.Append(ref)
	call := ir.NewIndirectCall(linkPos, entry.NewTemp(), ir.OrdinaryCall, ref.Dst())
	b.Append(call)
	b.SetTerm(ir.NewReturn(linkPos, call.Dst()))
	main := ir.NewModule("main.nomi")
	main.AddFunc(entry)
	other := ir.NewModule("other.nomi")
	other.AddFunc(linkConst(target, 42))
	wrong := ir.NewModule("wrong.nomi")
	wrong.AddFunc(linkConst(sameName, 7))
	got, err := vm.NewProgram(main, []*ir.Module{main, wrong, other}, io.Discard).Run("main")
	if err != nil || got != any(int64(42)) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := vm.NewProgram(main, []*ir.Module{main, wrong}, io.Discard).Run("main"); err == nil {
		t.Fatal("resolved a same-named declaration with a different symbol")
	}
}
