package vm_test

// THE LINK'S OWN UNITS: what `NewProgram` resolves, and what it refuses to
// resolve.
//
// `expectation_test.go`'s `TestVMExpectation_TheLinkIsWhatAddedTheThreeRecords`
// is the control over real programs. These two are the cases no lowering
// produces, so no population can reach them: a symbol TWO modules declare, and
// a machine opened by `New` behaving exactly as it did before linking existed.

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

var linkPos = ir.At("link.nomi", 1, 1)

func TestRunSymbol_DistinguishesSameNamedDeclarations(t *testing.T) {
	a, b := ir.NewSymbol("Person.greet"), ir.NewSymbol("Person.greet")
	mod := ir.NewModule("impl.nomi")
	mod.AddFunc(linkConst(a, 3))
	mod.AddFunc(linkConst(b, 7))
	m := vm.New(mod, io.Discard)
	for sym, want := range map[*ir.Symbol]int64{a: 3, b: 7} {
		got, err := m.RunSymbol(sym)
		v, ok := got.(int64)
		if err != nil || !ok || v != want {
			t.Fatalf("declaration %p returned %v, %v; want %d", sym, got, err, want)
		}
	}
	if _, err := m.Run("Person.greet"); err == nil {
		t.Fatal("name lookup accepted two declarations with the same name")
	}
	for _, sym := range []*ir.Symbol{nil, ir.NewSymbol("Person.greet")} {
		if _, err := m.RunSymbol(sym); err == nil {
			t.Fatal("accepted an entry declaration outside this module")
		}
	}
}

// linkConst is a zero-parameter function returning one Int, named by sym.
//
// ZERO PARAMETERS ON PURPOSE: `ir.Func.AddParam` is not called, so this file
// does not participate in that signature.
func linkConst(sym *ir.Symbol, v int64) *ir.Func {
	f := ir.NewFuncFor(linkPos, sym)
	b := f.NewBlock(linkPos, "entry")
	c := ir.NewInt(linkPos, f.NewTemp(), v)
	b.Append(c)
	b.SetTerm(ir.NewReturn(linkPos, c.Dst()))
	return f
}

// linkCaller is a zero-parameter function whose body calls callee and returns
// the result.
func linkCaller(sym, callee *ir.Symbol) *ir.Func {
	f := ir.NewFuncFor(linkPos, sym)
	b := f.NewBlock(linkPos, "entry")
	c := ir.NewCall(linkPos, f.NewTemp(), ir.OrdinaryCall, callee)
	b.Append(c)
	b.SetTerm(ir.NewReturn(linkPos, c.Dst()))
	return f
}

// TestLink_ACalleeInASiblingModuleResolvesByIdentityAndNotByName is the
// mechanism at its smallest, and the negative half is the point.
//
// Two modules, each declaring a function named `helper`, and the caller names
// ONE of them by symbol. Resolving by printed name would answer either; the
// machine must answer the one the caller named, and the test proves it by
// giving the two different return values.
func TestLink_ACalleeInASiblingModuleResolvesByIdentityAndNotByName(t *testing.T) {
	wanted := ir.NewSymbol("helper")
	decoy := ir.NewSymbol("helper")
	if wanted == decoy {
		t.Fatal("ir.NewSymbol returned one pointer for two calls, so this test cannot " +
			"tell identity from name")
	}

	entry := ir.NewModule("entry.nomi")
	entry.AddFunc(linkCaller(ir.NewSymbol("main"), wanted))

	right := ir.NewModule("right.nomi")
	right.AddFunc(linkConst(wanted, 7))
	wrong := ir.NewModule("wrong.nomi")
	wrong.AddFunc(linkConst(decoy, 9))

	// The decoy module comes FIRST, so a resolution that took the first
	// same-named function would answer 9.
	m := vm.NewProgram(entry, []*ir.Module{wrong, right}, io.Discard)
	got, err := m.Run("main")
	if err != nil {
		t.Fatalf("the linked call did not run: %v", err)
	}
	n, isInt := got.(int64)
	if !isInt {
		t.Fatalf("the call answered %T", got)
	}
	if n != 7 {
		t.Errorf("the linked call answered %d; the caller names the symbol `right.nomi` "+
			"declares, and `wrong.nomi` declares a DIFFERENT symbol with the same printed "+
			"name. A wrong answer here is resolution by name", n)
	}
}

// TestLink_ASymbolTwoModulesDeclareIsReportedRatherThanPicked is the fence
// `NewProgram` states at construction.
//
// NO PRODUCER CAN BUILD THIS: a callee symbol is interned on a declaration
// node and a node belongs to one unit. A hand-built set can, and picking one of
// two declarations for one identity is the failure `ir.Table.SelectOverload`
// refuses one layer up — so the machine reports instead.
func TestLink_ASymbolTwoModulesDeclareIsReportedRatherThanPicked(t *testing.T) {
	shared := ir.NewSymbol("helper")

	entry := ir.NewModule("entry.nomi")
	entry.AddFunc(linkCaller(ir.NewSymbol("main"), shared))

	a := ir.NewModule("a.nomi")
	a.AddFunc(linkConst(shared, 1))
	b := ir.NewModule("b.nomi")
	b.AddFunc(linkConst(shared, 2))

	_, err := vm.NewProgram(entry, []*ir.Module{a, b}, io.Discard).Run("main")
	if err == nil {
		t.Fatal("two modules declared one symbol and the machine picked one of them; " +
			"a Func's identity is its Symbol, so this is a silent wrong answer")
	}
	if !strings.Contains(err.Error(), "more than one") {
		t.Errorf("the ambiguity was reported as %v, which is not the ambiguity", err)
	}

	// AND THE CONTROL: the same caller with ONE of the two modules resolves,
	// so the report above is about the duplicate and not about the shape of
	// the graph.
	got, err := vm.NewProgram(entry, []*ir.Module{a}, io.Discard).Run("main")
	if err != nil {
		t.Fatalf("one declaring module did not resolve, so the ambiguity report above "+
			"may have been about something else: %v", err)
	}
	if n, ok := got.(int64); !ok || n != 1 {
		t.Errorf("the unambiguous link answered %v", got)
	}
}

// TestLink_AMachineOverOneModuleIsUnchanged pins that `New` did not acquire a
// second behaviour.
//
// The message a single-module machine reports for an unretained callee is the
// one it reported before linking existed, byte for byte, because
// `internal/irbuild`'s `vmClassify` reads a substring of it into its `LINKING`
// bucket and `internal/vm/testdata/nomatch`'s test reads it too.
func TestLink_AMachineOverOneModuleIsUnchanged(t *testing.T) {
	missing := ir.NewSymbol("helper")
	entry := ir.NewModule("entry.nomi")
	entry.AddFunc(linkCaller(ir.NewSymbol("main"), missing))

	_, err := vm.New(entry, io.Discard).Run("main")
	if err == nil {
		t.Fatal("a call to a symbol no module declares ran")
	}
	const want = "vm: main: helper names a declaration this module did not retain"
	if err.Error() != want {
		t.Errorf("a single-module machine reports %q; the text %q is read by\n"+
			"  internal/irbuild/vmretained_test.go's vmClassify (LINKING bucket)\n"+
			"  internal/vm/nomatch_test.go\n"+
			"so a change here moves a pin in another package", err.Error(), want)
	}

	// THE LINKED SPELLING IS DIFFERENT AND ALSO CARRIES THE SUBSTRING, which
	// is what keeps `vmClassify` classifying a linked failure at all.
	other := ir.NewModule("other.nomi")
	other.AddFunc(linkConst(ir.NewSymbol("unrelated"), 1))
	_, linkedErr := vm.NewProgram(entry, []*ir.Module{other}, io.Discard).Run("main")
	if linkedErr == nil {
		t.Fatal("a linked machine resolved a symbol no module in the set declares")
	}
	if linkedErr.Error() == want {
		t.Error("the linked failure reports the single-module sentence, so a reader " +
			"cannot tell a retention boundary from a program that has no such declaration")
	}
	if !strings.Contains(linkedErr.Error(), "did not retain") {
		t.Errorf("the linked failure reads %q and drops the substring vmClassify buckets "+
			"on, so a LINKING failure would classify as OTHER", linkedErr.Error())
	}
}
