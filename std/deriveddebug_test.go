package std

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestDerivedDebugCallsRecordTheirImpl: a std module that does not import
// Debug still registers the universal and derived Debug blocks the
// compiler writes for its types, under std's own Debug, so each
// `Debug.inspect` such a block's body calls on a field of a named type
// records the block it reaches (Callee.Impl), as the same call in a
// program does. A field of a function type has no block, and one of a
// type parameter is resolved by each instance. std/http and std/sessions
// write no `import std::debug`.
func TestDerivedDebugCallsRecordTheirImpl(t *testing.T) {
	lib := All()
	for _, name := range []string{"http", "sessions"} {
		if err := lib.Bodies(name); err != nil {
			t.Fatalf("std/%s: %v", name, err)
		}
		fa := lib.Files[name]
		if fa == nil {
			t.Fatalf("std has no module %q", name)
		}
		calls := 0
		for n, rc := range fa.Calls {
			call, ok := n.(*ast.Call)
			if !ok || rc == nil || !analysis.IsSynthesizedLine(call.Line) {
				continue
			}
			ce := rc.Callee
			if ce.Method != "inspect" || ce.Qualifier.Kind != analysis.QualInterface || ce.Qualifier.Std.Name != "Debug" {
				continue
			}
			if _, fn := ce.For.(*analysis.FuncType); fn || ce.For == nil || analysis.ContainsTypeParam(ce.For) {
				continue
			}
			calls++
			if ce.Impl == nil {
				t.Errorf("std/%s: a derived `Debug.inspect` of a %s records no impl block", name, ce.For)
			}
		}
		if calls == 0 {
			t.Fatalf("std/%s: no derived `Debug.inspect` call of a named type found, so this test checks nothing", name)
		}
	}
}
