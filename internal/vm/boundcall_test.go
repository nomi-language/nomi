package vm_test

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// boundCallProgram is an abstract module whose `read` calls Shout.shout
// through a type parameter's bound, and a plain module that implements
// Shout.shout for two types, A and B.
func boundCallProgram() (abstract, impls *ir.Module) {
	at := func(line int) ir.Pos { return ir.At("bound.nomi", line, 1) }
	shout := ir.NewLinkedSymbol("Shout.shout", "bound.Shout.shout")
	impls = ir.NewModule("impls.nomi")
	for i, name := range []string{"A", "B"} {
		f := ir.NewFuncFor(at(10+i), ir.NewLinkedSymbol(name+".shout", "bound."+name+".shout"))
		b := f.NewBlock(at(10+i), "entry")
		v := f.NewTemp()
		b.Append(ir.NewInt(at(10+i), v, int64(i)))
		f.SetType(v, ir.IntType)
		b.SetTerm(ir.NewReturn(at(10+i), v))
		impls.AddFunc(f)
		impls.Implement(shout, ir.NewLinkedSymbol(name, "bound."+name), f.Sym())
	}
	abstract = ir.NewModule("bound.nomi")
	abstract.SetAbstract()
	f := ir.NewFuncFor(at(1), ir.NewLinkedSymbol("read", "bound.read"))
	b := f.NewBlock(at(1), "entry")
	got := f.NewTemp()
	b.Append(ir.NewBoundCall(at(2), got, ir.OrdinaryCall, shout))
	f.SetType(got, ir.IntType)
	b.SetTerm(ir.NewReturn(at(3), got))
	abstract.AddFunc(f)
	return abstract, impls
}

// The walk reads a call through a bound as it reads a dispatch: it reaches
// every implementation the program retains.
func TestBoundCall_TheWalkReachesEveryImplementation(t *testing.T) {
	abstract, impls := boundCallProgram()
	read := abstract.Funcs()[0]
	var names []string
	for _, f := range vm.ReachedFuncs(abstract, []*ir.Module{impls}, []*ir.Func{read}, nil) {
		names = append(names, f.Name())
	}
	if want := []string{"read", "A.shout", "B.shout"}; !slices.Equal(names, want) {
		t.Fatalf("the walk from read reaches %v, want %v", names, want)
	}
}

// No machine runs a call through a bound: its compiler compiles one to a
// failure that says what it is.
func TestBoundCall_NoMachineRunsOne(t *testing.T) {
	abstract, impls := boundCallProgram()
	_, err := vm.NewProgram(abstract, []*ir.Module{impls}, io.Discard).Run("read")
	if err == nil || !strings.Contains(err.Error(), "a call through a bound, Shout.shout, is in an abstract body, which no machine runs") {
		t.Fatalf("running read = %v, want the bound call's failure", err)
	}
}
