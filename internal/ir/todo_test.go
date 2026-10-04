package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// todoFunc is `fn f(): Int { todo "why" }`: a trap whose destination is the
// function's result, read after it by the return.
func todoFunc(reason string) *ir.Func {
	const file = "todo.nomi"
	f := ir.NewFunc(ir.At(file, 1, 1), "f")
	b := f.NewBlock(ir.At(file, 1, 1), "entry")
	v := f.NewTemp()
	b.Append(ir.NewTodo(ir.At(file, 2, 5), v, reason))
	f.SetType(v, ir.IntType)
	b.SetTerm(ir.NewReturn(ir.At(file, 3, 1), v))
	return f
}

func TestTodo_AlwaysFaultsAndDefinesItsDestination(t *testing.T) {
	f := todoFunc("parse the header")
	in := f.Blocks()[0].Instrs()[0]
	if got := ir.InstrFaults(in); got != ir.FaultTodo || got.String() != "todo" {
		t.Fatalf("faults %v, want todo", got)
	}
	if got := in.String(); got != `t1 = todo "parse the header"` {
		t.Errorf("String() = %q", got)
	}
	if got := todoFunc("").Blocks()[0].Instrs()[0].String(); got != "t1 = todo" {
		t.Errorf("bare String() = %q", got)
	}
	// The return reads the destination, which the trap defines, so the graph
	// lints clean.
	if err := ir.Lint(f); err != nil {
		t.Fatalf("lint: %v", err)
	}
}

func TestTodo_RoundTripsThroughAnImage(t *testing.T) {
	m := ir.NewModule("todo.nomi")
	m.AddFunc(todoFunc("why"))
	data, err := ir.EncodeImage(ir.Image{Modules: []*ir.Module{m}, Entry: 0})
	if err != nil {
		t.Fatal(err)
	}
	back, err := ir.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	td, ok := back.Modules[0].Funcs()[0].Blocks()[0].Instrs()[0].(*ir.Todo)
	if !ok {
		t.Fatalf("decoded %T", back.Modules[0].Funcs()[0].Blocks()[0].Instrs()[0])
	}
	if td.Reason() != "why" || td.Pos().Line() != 2 || td.Pos().Col() != 5 || td.Dst() == ir.NoTemp {
		t.Fatalf("decoded %v at %v", td, td.Pos())
	}
}
