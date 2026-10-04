package vm_test

// Operator syntax and explicit scalar impl calls both retain positioned
// arithmetic. Each must blame its own source line, including when execution
// starts from main and reaches the fault through a retained call.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

func faultPosPath(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", dir, "main.nomi"))
	if err != nil {
		t.Fatalf("resolving the reproduction: %v", err)
	}
	return p
}

// faultPosArith lowers one fixture and answers the retained modules plus every
// `ir.Arith` node that reports a fault, with the line it names.
func faultPosArith(t *testing.T, dir string) (mods []*ir.Module, faults map[string]int) {
	t.Helper()
	prog, err := irbuild.Analyze(faultPosPath(t, dir))
	if err != nil {
		t.Fatalf("analyzing %s: %v", dir, err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("%s must lower, so the compiled engine exists: %v", dir, err)
	}
	faults = map[string]int{}
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					a, ok := in.(*ir.Arith)
					if !ok || a.Faults() == 0 {
						continue
					}
					faults[f.Name()] = a.Pos().Line()
				}
			}
		}
	}
	return res.IR, faults
}

// TestFaultPos_TheOperatorSpellingReachesThisEngineAndBlamesItsOwnLine is the
// gate. `big + 1` on line 7 must retain and must fault at 7.
func TestFaultPos_TheOperatorSpellingReachesThisEngineAndBlamesItsOwnLine(t *testing.T) {
	mods, faults := faultPosArith(t, "faultposop")
	if len(mods) == 0 {
		t.Fatal("the operator spelling no longer retains an ir.Module, so this " +
			"engine has no answer for an integer overflow at all")
	}
	line, retained := faults["overflow_via_operator"]
	if !retained {
		t.Fatal("no faulting ir.Arith was retained for overflow_via_operator")
	}
	// `big + 1` is on line 7 of testdata/faultposop/main.nomi. Asserted as a
	// literal because the fixture is this package's own and an edit above the
	// operator is meant to fail here.
	if line != 7 {
		t.Errorf("the operator's ir.Arith names line %d; `big + 1` is on line 7", line)
	}

	var out strings.Builder
	_, err := vm.New(mods[0], &out).Run("overflow_via_operator")
	if err == nil {
		t.Fatal("MaxInt64 + 1 must fault on this engine")
	}
	const want = "line 7: integer overflow: 9223372036854775807 + 1"
	if err.Error() != want {
		t.Errorf("this engine's fault text:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestFaultPos_TheImplSpellingReachesThisEngineAndBlamesItsOwnLine(t *testing.T) {
	mods, faults := faultPosArith(t, "faultpos")
	if len(mods) != 1 || faults["overflow_via_impl"] != 7 {
		t.Fatalf("impl fixture: modules=%d, arithmetic=%v", len(mods), faults)
	}
	var out strings.Builder
	_, err := vm.New(mods[0], &out).Run("main")
	const want = "line 7: integer overflow: 9223372036854775807 + 1"
	if err == nil || err.Error() != want || out.String() != "" {
		t.Fatalf("fault = %v; output %q", err, out.String())
	}
}
