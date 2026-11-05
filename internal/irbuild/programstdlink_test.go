package irbuild

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

const linkedStdProgram = `import std::uuid.Uuid

fn main() {
    id = Uuid.nil()
    dbg Uuid.version(id)
    dbg "${id}"
}
`

// lowerLinkedStdProgram lowers linkedStdProgram once and runs it, answering
// its modules and its output.
func lowerLinkedStdProgram(t *testing.T) (*Result, string) {
	t.Helper()
	return lowerStdProgram(t, false)
}

// lowerStdProgram is lowerLinkedStdProgram, lowering the listed modules with
// the program when own is set.
func lowerStdProgram(t *testing.T, own bool) (*Result, string) {
	t.Helper()
	if linksThroughObjects() {
		// Each program then links its own decoded copy of the kept modules.
		t.Skip("lowerings link through objects")
	}
	p, err := AnalyzeSource("linked", linkedStdProgram)
	if err != nil {
		t.Fatal(err)
	}
	p.noLinkStd = own
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	mods := res.IRModules()
	m := vm.NewProgram(mods[0], mods, &out)
	if _, err := m.Run("main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	return res, out.String()
}

// moduleNamed is the module of mods whose name ends in suffix, or nil.
func moduleNamed(mods []*ir.Module, suffix string) *ir.Module {
	for _, m := range mods {
		if strings.HasSuffix(m.Name(), suffix) {
			return m
		}
	}
	return nil
}

// The listed std modules are lowered once for the process and linked by
// name into every program that reaches them: two such programs link the
// same modules, neither lowers a body of std/uuid itself, and both print
// what the program lowered with its listed modules printed.
func TestLinkedStd_ProgramsShareOneLoweringOfTheirListedModules(t *testing.T) {
	_, want := lowerStdProgram(t, true)
	a, outA := lowerLinkedStdProgram(t)
	b, outB := lowerLinkedStdProgram(t)
	if outA != want || outB != want {
		t.Fatalf("a linked program printed\n%s\nand\n%s\nwhere the program lowered with its listed modules printed\n%s", outA, outB, want)
	}
	keptA := moduleNamed(a.irLibraries, "uuid.nomi")
	keptB := moduleNamed(b.irLibraries, "uuid.nomi")
	if keptA == nil || keptA != keptB {
		t.Fatalf("std/uuid's module is %p in one program and %p in the next; both link the one kept lowering", keptA, keptB)
	}
	for _, res := range []*Result{a, b} {
		if m := moduleNamed(res.IR, "uuid.nomi"); m != nil && len(m.Funcs()) > 0 {
			t.Fatalf("the program lowered %d of std/uuid's bodies itself (%s); a linked module's bodies are the kept lowering's",
				len(m.Funcs()), m.Funcs()[0].Name())
		}
	}
	// The program's call reaches the kept function: one symbol, by name.
	var call *ir.Symbol
	for _, f := range a.IR[0].Funcs() {
		for _, blk := range f.Blocks() {
			for _, in := range blk.Instrs() {
				if c, ok := in.(*ir.Call); ok && strings.HasPrefix(c.Callee().Link(), "std/uuid.Uuid") && strings.HasSuffix(c.Callee().Link(), ".nil") {
					call = c.Callee()
				}
			}
		}
	}
	if call == nil || keptA.FuncFor(call) == nil {
		t.Fatalf("main's call of Uuid.nil names %v, which the kept std/uuid does not define", call)
	}
}

// The listed modules a program links are named by declaration, so a
// program that lowers its own copy names them as the kept one does.
func TestLinkedStd_NamesDoNotDependOnTheLowering(t *testing.T) {
	first, _ := lowerStdProgram(t, true)
	linked, _ := lowerLinkedStdProgram(t)
	names := func(mods []*ir.Module) map[string]bool {
		out := map[string]bool{}
		for _, m := range mods {
			for _, f := range m.Funcs() {
				if l := f.Sym().Link(); strings.HasPrefix(l, "std/uuid.") {
					out[l] = true
				}
			}
		}
		return out
	}
	kept := names(linked.irLibraries)
	for l := range names(first.IRModules()) {
		if !kept[l] && !strings.Contains(l, "<") {
			t.Errorf("%s is defined by a program that lowers std/uuid itself and not by the kept lowering", l)
		}
	}
	if len(kept) == 0 {
		t.Fatal("the kept lowering defines no std/uuid function")
	}
}
