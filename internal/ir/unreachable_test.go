package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// unreachableFunc is `fn f() -> Int { unreachable "why" }`: a trap whose
// destination is the function's result, read after it by the return.
func unreachableFunc(reason string) *ir.Func {
	const file = "unreachable.nomi"
	f := ir.NewFunc(ir.At(file, 1, 1), "f")
	b := f.NewBlock(ir.At(file, 1, 1), "entry")
	v := f.NewTemp()
	b.Append(ir.NewUnreachable(ir.At(file, 2, 5), v, reason))
	f.SetType(v, ir.IntType)
	b.SetTerm(ir.NewReturn(ir.At(file, 3, 1), v))
	return f
}

func TestUnreachable_AlwaysFaultsAndDefinesItsDestination(t *testing.T) {
	f := unreachableFunc("every student is listed")
	in := f.Blocks()[0].Instrs()[0]
	if got := ir.InstrFaults(in); got != ir.FaultUnreachable || got.String() != "unreachable" {
		t.Fatalf("faults %v, want unreachable", got)
	}
	if got := in.String(); got != `t1 = unreachable "every student is listed"` {
		t.Errorf("String() = %q", got)
	}
	if err := ir.Lint(f); err != nil {
		t.Fatalf("lint: %v", err)
	}
}

func TestUnreachable_RoundTripsThroughAnImage(t *testing.T) {
	m := ir.NewModule("unreachable.nomi")
	m.AddFunc(unreachableFunc("why"))
	data, err := ir.EncodeImage(ir.Image{Modules: []*ir.Module{m}, Entry: 0})
	if err != nil {
		t.Fatal(err)
	}
	back, err := ir.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := back.Modules[0].Funcs()[0].Blocks()[0].Instrs()[0].(*ir.Unreachable)
	if !ok {
		t.Fatalf("decoded %T", back.Modules[0].Funcs()[0].Blocks()[0].Instrs()[0])
	}
	if u.Reason() != "why" || u.Pos().Line() != 2 || u.Pos().Col() != 5 || u.Dst() == ir.NoTemp {
		t.Fatalf("decoded %v at %v", u, u.Pos())
	}
}
