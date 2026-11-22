package std

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestDerivedDebugCallsRecordTheirImpl: each `Debug.inspect` a derived
// Debug body in a std module calls on a field of a named type records how
// it is answered, as the same call in a program does: the block it reaches
// (Callee.Impl) for a type with a Debug block, under std's own Debug, and
// its shape for a type with none. A field of a function type has no
// block, and one of a type parameter is resolved by each instance.
// std/xml's Attribute holds a Name, whose Debug is derived too.
func TestDerivedDebugCallsRecordTheirImpl(t *testing.T) {
	lib := All()
	for _, name := range []string{"storage", "xml"} {
		if err := lib.Bodies(name); err != nil {
			t.Fatalf("std/%s: %v", name, err)
		}
		fa := lib.Files[name]
		if fa == nil {
			t.Fatalf("std has no module %q", name)
		}
		calls, byImpl := 0, 0
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
			switch {
			case ce.Form == analysis.OperatorByImpl && ce.Impl != nil:
				byImpl++
			case ce.Form == analysis.OperatorStructural && ce.Impl == nil:
			default:
				t.Errorf("std/%s: a derived `Debug.inspect` of a %s is Form %d with impl %v, want its block or its shape", name, ce.For, ce.Form, ce.Impl)
			}
		}
		if calls == 0 || byImpl == 0 {
			t.Fatalf("std/%s: %d derived `Debug.inspect` calls of a named type found, %d of a type with a block, so this test checks nothing", name, calls, byImpl)
		}
	}
}
