package analysis_test

import "testing"

// `self` in an interface method's type position (`fn hello(value: self)`)
// records a hover / go-to-def reference resolving to the interface, rather
// than being a dead token. Mirrors the impl-block `self`, which resolves to
// the concrete receiver.
func TestInterfaceSelfTypeRecordsReference(t *testing.T) {
	src := `interface Greet {
  fn hello(value: self): String
}
fn main() {
  Unit
}`
	fa, _ := checkSourceWithStdlib(src)
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "self" {
			if sym.Resolved == nil || sym.Resolved.Name != "Greet" {
				t.Fatalf("`self` reference should resolve to interface Greet, got %+v", sym.Resolved)
			}
			return
		}
	}
	t.Error("expected `self` in the interface method signature to record a reference")
}
