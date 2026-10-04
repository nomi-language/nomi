package analysis

import "testing"

func TestSameOwner_LocalCallableHasItsOwnSignature(t *testing.T) {
	for _, body := range []string{
		`read = |_n: Int| "local"; read(3)`,
		`read = |_n: Int| "local"; 3 |> read()`,
	} {
		_, errs := checkSource(`struct Box { value: Int }
impl Box {
 fn read(b: Box): Int { b.value }
 fn use(_b: Box): String { ` + body + ` }
}`)
		if len(errs) != 0 {
			t.Fatalf("%s: %v", body, errs)
		}
	}
}

func TestSameOwner_LocalValueKeepsItsBinding(t *testing.T) {
	fa, _ := checkSource(`struct Box { value: Int }
impl Box {
 fn read(b: Box): Int { b.value }
 fn use(b: Box): Int { read = 7; read(b) }
}`)
	found := false
	for pos, sym := range fa.References {
		if pos.Line == 4 && sym.Name == "read" {
			found = true
			if sym.Kind != SymbolBinding {
				t.Fatalf("local value resolved to %v", sym.Kind)
			}
		}
	}
	if !found {
		t.Fatal("missing reference to local read")
	}
}
