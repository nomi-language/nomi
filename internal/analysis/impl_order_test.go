package analysis

import "testing"

// Declaration order must not matter for impl-interface resolution in the
// single-file analysis path (buildModule), matching BuildProject's
// stubs-everywhere-first / annotations-second sweeps. These tests pin the
// regression where a (top-level or lowered nested) impl block declared
// BEFORE its interface errored with "impl block: undefined interface".

// TestImplBlock_TopLevel_InterfaceDeclaredLater: a hand-written top-level
// impl whose interface is declared later in the same file resolves cleanly.
func TestImplBlock_TopLevel_InterfaceDeclaredLater(t *testing.T) {
	src := `pub struct Dog { name: String }

impl Speech for Dog {
    fn speak(d: Dog): String { d.name }
}

pub interface Speech {
    fn speak(value: self): String
}`
	fa, errs := buildTypesFromSource(src)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if len(all) > 0 {
		t.Fatalf("unexpected errors: %s", joinErrs(all))
	}
	if fa.Impls == nil || !fa.Impls["Dog"]["Speech"] {
		t.Fatalf("expected Impls[Dog][Speech], got %v", fa.Impls)
	}
}

// TestImplBlock_InterfaceDeclaredLater: an `impl Speech for Dog { }` whose
// interface is declared later in the file resolves cleanly.
func TestImplBlock_InterfaceDeclaredLater(t *testing.T) {
	src := `pub struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name
  }
}

pub interface Speech {
  fn speak(value: self): String
}

`
	fa, errs := buildTypesFromSource(src)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if len(all) > 0 {
		t.Fatalf("unexpected errors: %s", joinErrs(all))
	}
	if fa.Impls == nil || !fa.Impls["Dog"]["Speech"] {
		t.Fatalf("expected Impls[Dog][Speech], got %v", fa.Impls)
	}
}
