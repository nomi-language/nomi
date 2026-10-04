package irbuild

// ONE DECLARATION, ONE `ir.Symbol`, ACROSS TWO COMPILATION UNITS.
//
// `ir.Table` is per gen — `irtable.go`'s own comment says "this compilation
// unit's IR type and declaration table" — so the call site in unit A and
// `irFuncShellFor` in unit B interned two symbols for one sibling `fn`. Nothing
// noticed, because the Go-spelled read-back never reads a callee symbol: the
// Go target travels beside the node on `irScalarSide.call`. `internal/vm` reads it, and
// `ir.Module.FuncFor` resolves by POINTER, so every cross-unit call reported
// "names a declaration this module did not retain" — three tour expectation
// records' worth.
//
// `irSiblingCalleeSym` interns the callee in the DECLARING unit's table off the
// declaration node, which is the same rule `resolveSignatures` already states
// for the kinds: "the kinds produced belong to the DECLARING gen".
//
// WHY THIS TEST AND NOT A TEXT COMPARISON. The Go-spelled `code` for
// `math.double(7)` is `nomimod1.NomiFn_double(fr, 7)` whichever symbol the
// node carries, so a regression to two symbols leaves that text unchanged
// and only a POINTER COMPARISON catches it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

const irSiblingLinkLib = `pub fn double(n: Int): Int {
  n * 2
}
`

const irSiblingLinkEntry = `import {
  std/io
  lib
}

fn main() {
  io.print("${lib.double(7)}")
}
`

// irSiblingLinkFixture is the two-file program, entry first.
func irSiblingLinkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	for name, src := range map[string]string{
		"main.nomi": irSiblingLinkEntry,
		"lib.nomi":  irSiblingLinkLib,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return entry
}

// TestIRSiblingLink_ACrossUnitCalleeAndItsDeclarationShareOneSymbol is the
// producer half of the link.
//
// THE ASSERTION IS POINTER IDENTITY AND THE NEGATIVE IS SPELLED OUT: two
// symbols with the printed name `double` would satisfy every name-based check
// and still leave the VM unable to resolve the call, which is the state this
// closed. So the test compares the `ir.Call`'s callee against the retained
// `ir.Func`'s own `Sym()` with `!=`, and separately asserts that `FuncFor`
// answers — because `FuncFor` is the consumer's actual question and a symbol
// that matched while `FuncFor` declined would mean the function was never
// added to its module.
func TestIRSiblingLink_ACrossUnitCalleeAndItsDeclarationShareOneSymbol(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	prog, err := Analyze(irSiblingLinkFixture(t))
	if err != nil {
		t.Fatalf("analysing the two-file program: %v", err)
	}
	res, _, err := GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating the two-file program: %v", err)
	}
	if len(res.IR) < 2 {
		t.Fatalf("the program retained %d module(s); this reading needs the caller and "+
			"the callee in DIFFERENT modules, so the fixture no longer states the "+
			"property", len(res.IR))
	}

	var (
		call     *ir.Call
		callerIn *ir.Module
		callee   *ir.Func
		calleeIn *ir.Module
	)
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "double" {
				callee, calleeIn = f, m
			}
			if f.Name() != "main" {
				continue
			}
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, isCall := in.(*ir.Call); isCall && !c.Crosses() {
						call, callerIn = c, m
					}
				}
			}
		}
	}
	if call == nil {
		t.Fatal("`main` retains no non-crossing ir.Call, so `lib.double(7)` did not build " +
			"and this test has no subject")
	}
	if callee == nil {
		t.Fatal("`double` is not retained in any module, so there is nothing for the call " +
			"to name and the link could not be checked either way")
	}
	if callerIn == calleeIn {
		t.Fatal("the caller and the callee landed in ONE module, so `FuncFor` would answer " +
			"without any link and this test would pass vacuously")
	}

	if call.Callee() != callee.Sym() {
		t.Errorf("the call names symbol %p (%q) and the declaration carries %p (%q). "+
			"ONE DECLARATION MUST HAVE ONE SYMBOL: ir.Module.FuncFor resolves by pointer, "+
			"so two symbols make every cross-unit call unresolvable for internal/vm while "+
			"the Go-spelled code text stays unchanged. See irSiblingCalleeSym.",
			call.Callee(), call.Callee().Name(), callee.Sym(), callee.Sym().Name())
	}
	if got := calleeIn.FuncFor(call.Callee()); got != callee {
		t.Errorf("the declaring module's FuncFor answered %v for the symbol the call "+
			"names; the consumer asks exactly this question", got)
	}
	if got := callerIn.FuncFor(call.Callee()); got != nil {
		t.Errorf("the CALLING module answered %v for a declaration it does not hold, so "+
			"the two modules are not distinct containers and the link is not what "+
			"resolves the call", got)
	}
	if name := call.Callee().Name(); name != "double" {
		t.Errorf("the shared symbol prints as %q; both sides must OFFER the declaration's "+
			"own name, because ir.Table.Symbol keeps the FIRST name offered and which unit "+
			"is built first is not a property of the program", name)
	}
}
