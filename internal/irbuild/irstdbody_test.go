package irbuild

// Retention of the stdlib's function bodies.

import (
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// irStdWantRetained is how many stdlib graphs the builder builds and keeps.
// vmStdWantRetained adds the arity wrappers and slot accessors to it, and
// TestIRRetainedStdPopulationRuns reads that total off the cached modules.
// Re-derive it when the builder retains more or less of std, and name the
// bodies that moved in the commit message.
const irStdWantRetained = 374

// TestIRStdFunc_ASiblingCallIsRetainedOnBothSides is the `call` audit.
//
// The same Nomi source retains its sibling call in a user module and in a
// stdlib module. A std gen's `g.funcs` is empty (`newStdGen` never fills it),
// so the std side resolves a bare callee through `stdlibSibling` and links to
// the sibling's retained declaration symbol when that body is already in the
// module (see irStdSiblingSym). TestIRStdSibling_BareCallsLinkToEarlierBodies checks the
// symbol, the VM run and the Go read-back.
func TestIRStdFunc_ASiblingCallIsRetainedOnBothSides(t *testing.T) {
	const body = "fn helper(n: Int): Int {\n  n + 1\n}\n\npub fn use_it(n: Int): Int {\n  helper(n)\n}\n"

	t.Run("a user module builds an ir.Call", func(t *testing.T) {
		var c irFuncRetentionCount
		defer c.observe(t, irFromModule)()
		lowerIROnly(t, irFormSource(body))
		if !slicesContain(c.retained, "helper") {
			t.Fatalf("`helper` was not retained, so the control says nothing: retained %v",
				c.retained)
		}
		if !slicesContain(c.retained, "use_it") {
			t.Fatalf("`use_it` was not retained in a user module: retained %v, walked %v",
				c.retained, c.walked)
		}
		if !slicesContain(c.readBack, "use_it") {
			t.Error("`use_it` was not read back; `irfuncform.go` spells an ir.Call, so a " +
				"user module's sibling call is retained and read back")
		}
	})

	t.Run("a stdlib module builds one too", func(t *testing.T) {
		var c irFuncRetentionCount
		defer c.observe(t, irFromStd)()
		lowerStdSourceOnly(t, body)
		if !slicesContain(c.retained, "irstdcall.helper") {
			t.Fatalf("`helper` was not retained on the std side, so this row measures the "+
				"harness and not the callee resolution: retained %v, walked %v",
				c.retained, c.walked)
		}
		if !slicesContain(c.retained, "irstdcall.use_it") {
			t.Errorf("`use_it` was not retained on the std side although its sibling "+
				"`helper` is declared earlier and retained: retained %v, walked %v",
				c.retained, c.walked)
		}
	})
}

// lowerStdSourceOnly lowers src as a stdlib module and discards the text.
//
// It asks a std-side shape question of one source rather than of the whole
// stdlib. `stdModuleContext` is the shared prologue; see stdmodulegen.go on
// why a test must not reassemble it by hand.
func lowerStdSourceOnly(t *testing.T, src string) {
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
	lowerStdlibModule(v, empty)
}
