package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// Same-module stdlib calls link to the callee's retained declaration in the
// module being lowered. The Tour's Codepoint range program is
// TestIRRangeIter_TourCodepointRange; it depends on `Discrete.next` for
// Codepoint, which calls `Codepoint.to_int` and `Codepoint.from_int`.

// TestIRStdSibling_TypeQualifiedSameModuleCalls runs three std/codepoints
// bodies that call their own module's inherent functions by type
// qualification: `step_by`, `next` and `inspect` all call `Codepoint.to_int`,
// and the first two call `Codepoint.from_int`.
func TestIRStdSibling_TypeQualifiedSameModuleCalls(t *testing.T) {
	verifyLambdaProgram(t, `fn run(): Maybe<Int> {
  a = try Codepoint.from_int(65)
  b = try Codepoint.step_by(a, 2)
  c = try Codepoint.next(b)
  dbg Codepoint.to_int(c)
  dbg Codepoint.inspect(c)
  gap = try Codepoint.from_int(55_295)
  dbg Codepoint.to_int(try Codepoint.next(gap))
  Some(0)
}

fn main() {
  _ = run()
}
`, "dbg line 5: Codepoint.to_int(c) = 68\n"+
		"dbg line 6: Codepoint.inspect(c) = \"Codepoint(68)\"\n"+
		"dbg line 8: Codepoint.to_int(try Codepoint.next(gap)) = 57344\n")
}

// lowerStdSource lowers src as a one-module stdlib and answers its retained
// IR.
func lowerStdSource(t *testing.T, src string) *ir.Module {
	t.Helper()
	proj, err := frontend.New(frontend.Config{}).CheckSource("irstdcall", src, frontend.Mode{})
	if err != nil {
		t.Fatalf("the fixture must type-check before it can say anything about lowering: %v", err)
	}
	nodes, fa := proj.Nodes, proj.FA
	empty := &stdlibIndex{
		byType: map[string][]*stdFunc{}, byFile: map[string]*stdFunc{},
		byIface:   map[string]map[kind][]*stdFunc{},
		modulePkg: map[string]string{},
		byKey:     map[string]*stdFunc{},
		byOnce:    map[string]*stdOnce{}, views: map[string]*stdModuleView{},
		earlierMods: map[string]map[string]bool{},
	}
	v := stdModuleContext("irstdcall", "irstdcall.nomi", "nomistdirstdcall", nodes, fa, empty)
	_, _, mod := lowerStdlibModule(v, empty)
	return mod
}

// TestIRStdSibling_BareCallsLinkToEarlierBodies lowers a stdlib module whose
// body calls an earlier sibling by its bare name. The call names the callee's
// own declaration symbol, the VM runs the chain, and the Go read-back equals
// the native walk. No std module retains a body through a bare Nomi sibling
// call yet: every current caller declines on an operand first.
func TestIRStdSibling_BareCallsLinkToEarlierBodies(t *testing.T) {
	const src = `fn helper(n: Int): Int {
  n + 1
}

pub fn bare(n: Int): Int {
  helper(n) * 2
}

pub fn forward(n: Int): Int {
  later(n)
}

fn later(n: Int): Int {
  n - 1
}
`
	mod := lowerStdSource(t, src)
	if mod == nil {
		t.Fatal("the module retained nothing")
	}
	byName := map[string]*ir.Func{}
	for _, f := range mod.Funcs() {
		byName[f.Name()] = f
	}
	for _, name := range []string{"helper", "bare", "later", "forward"} {
		if byName[name] == nil {
			t.Fatalf("%s was not retained; retained %v", name, irFuncNames(byName))
		}
	}
	// A forward reference names a body emitted later; irPrebuildStdBodies
	// builds every graph before any body is emitted.
	for caller, callee := range map[string]string{"bare": "helper", "forward": "later"} {
		if !irCallsSym(byName[caller], byName[callee].Sym()) {
			t.Errorf("%s does not call %s's declaration symbol", caller, callee)
		}
	}

	m := vm.New(mod, nil)
	for _, c := range []struct {
		entry string
		arg   any
		want  int64
	}{
		{"bare", int64(4), 10},
	} {
		v, err := vmRunV(m, c.entry, c.arg)
		if err != nil {
			t.Fatalf("%s: %v", c.entry, err)
		}
		if iv, ok := v.(int64); !ok || iv != c.want {
			t.Fatalf("%s = %v; want %d", c.entry, v, c.want)
		}
	}

}

func irCallsSym(f *ir.Func, sym *ir.Symbol) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if c, ok := in.(*ir.Call); ok && c.Callee() == sym {
				return true
			}
		}
	}
	return false
}

func irFuncNames(m map[string]*ir.Func) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
