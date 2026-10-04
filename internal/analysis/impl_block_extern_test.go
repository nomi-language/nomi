package analysis

import (
	"testing"
)

// A `host fn` inside an `impl Iface for Type` block IS the
// interface-method impl — it must feed the same conformance structures
// (Impls + DispatchNames + the per-method override recording) a pure-Nomi
// `fn` item does, so DetectMissingImpls treats the interface as satisfied
// and dispatch is recorded.
func TestImplBlock_InterfaceExtern_RegistersConformance(t *testing.T) {
	src := `pub interface Formatted {
    fn format(value: self): String
}

pub struct User { name: String }

impl Formatted for User {
    host fn format(user: User): String
}`

	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if fa.Impls == nil || !fa.Impls["User"]["Formatted"] {
		t.Fatalf("expected Impls[User][Formatted], got %v", fa.Impls)
	}
	if !fa.DispatchNames["format"] {
		t.Fatalf("expected DispatchNames[format] for an extern interface method, got %v", fa.DispatchNames)
	}
}

// A top-level `host fn` registers as an ordinary module function.
func TestImplBlock_TopLevelExternFunctionRegisters(t *testing.T) {
	src := `pub struct Widget {
  n: Int
}

pub host fn make(n: Int): Widget
`

	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	sym := fa.ModuleScope.Lookup("make")
	if sym == nil || sym.Kind != SymbolFunction {
		t.Fatalf("expected module function make, got %v", sym)
	}
}
