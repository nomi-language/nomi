package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/std"
)

// stdSiblingBindsPerModule is how many sibling bindings lowering one stdlib
// module once may make: newStdGen's (onces and prompt cases), the
// prebuild's and lowerStdlibModule's body loop. Each is one binding for the
// whole phase.
const stdSiblingBindsPerModule = 3

// TestStdSiblingBinds_OneStdlibLoweringBindsAFixedNumberPerModule holds the
// number of sibling bindings in one stdlib lowering to a constant per module
// lowering. A member of an import cycle is lowered once per pass over its
// cycle (lowerStdUnit), and each pass is one lowering of it.
//
// A binding rebuilds the local index, the underlay of every earlier module
// and every sibling's name, so rebinding once per BODY ATTEMPT in every
// fixed-point round, or once per body, costs well over a thousand bindings per
// lowering and a large share of every `nomi run`. The answer stays right and
// only the time is wrong, so no output check can see a regression here. See
// irPrebuildStdBodies.
//
// Sequential, not t.Parallel: the counters are process-wide, and a parallel
// test lowering a program in the same window would add its own bindings.
func TestStdSiblingBinds_OneStdlibLoweringBindsAFixedNumberPerModule(t *testing.T) {
	modules := 0
	for _, nodes := range std.Load().Nodes {
		if len(nodes) > 0 {
			modules++
		}
	}
	before, lowerings := stdSiblingBinds.Load(), stdModuleLowerings.Load()
	buildStdlibIndex()
	n := int(stdSiblingBinds.Load() - before)
	lowered := int(stdModuleLowerings.Load() - lowerings)
	// The positive control: every module with a body binds at least once, so
	// zero means the counter is not counting.
	if n < 1 {
		t.Fatalf("one stdlib lowering made %d sibling bindings; stdSiblingBinds is not counting, "+
			"so this test measures nothing", n)
	}
	if lowered < modules/2 {
		t.Fatalf("one stdlib lowering lowered %d module(s) of %d; stdModuleLowerings is not counting", lowered, modules)
	}
	if budget := stdSiblingBindsPerModule * lowered; n > budget {
		t.Fatalf("one stdlib lowering made %d sibling bindings over %d module lowerings; the budget is %d "+
			"(%d per lowering).\nA binding rebuilds the module's local index and the underlay of every "+
			"earlier module. Look for bindStdSiblings called per body or per fixed-point attempt "+
			"instead of once per phase (internal/irbuild/stdlib.go, irPrebuildStdBodies and "+
			"lowerStdlibModule's body loop).", n, lowered, budget, stdSiblingBindsPerModule)
	}
	t.Logf("%d sibling bindings over %d module lowerings of %d modules", n, lowered, modules)
}
