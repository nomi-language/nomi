package irbuild

import (
	"testing"
)

// genericIfaceHead declares a generic interface, a generic struct, and an impl
// of the first for the second — `testdata/iface_cascade.nomi`'s shape, reused
// so the two cannot drift about what "a generic interface" is.
const genericIfaceHead = `interface Chooser<T> {
  fn left(p: self): T
  fn right(p: self): T
}

struct Pair<E> {
  a: E
  b: E
}

impl Chooser for Pair<E> {
  fn left(p: Pair<E>): E {
    p.a
  }

  fn right(p: Pair<E>): E {
    p.b
  }
}

`

// TestGenericIface_IsStillNotErasable: a generic interface is neither erasable
// nor implementable.
//
// A generic interface declaration is quiet (`useSiteOnly`) but `lowerable`
// stays false, so every route that could produce a wrong answer refuses. That
// is a claim about four readers, asserted here:
//
//   - `typeOf`'s interface arm, which turns a name into `existential(d)` —
//     `Chooser<Int>` and `Chooser<String>` would be ONE existential kind and one
//     dispatch table, because `existential` takes its identity from the pointer;
//   - `inferred.go`'s `solvedIface` arm, the inference-side twin of that;
//   - `registerImplDef`, which refuses an impl block of an interface whose
//     requirements nothing has checked;
//   - `resolveIfaces`, which does not resolve one, so `d.order` is empty and no
//     table can be minted for any of its methods.
func TestGenericIface_IsStillNotErasable(t *testing.T) {
	g := lowerableGen(t, genericIfaceHead+"fn main() {\n}\n")
	d := g.ifaces["Chooser"]
	if d == nil {
		t.Fatal("no `Chooser` def")
	}
	if !d.useSiteOnly {
		t.Fatal("`Chooser` is not marked useSiteOnly, so the declaration is not the quiet one this test is about")
	}
	if d.lowerable {
		t.Error("`Chooser` is LOWERABLE. A quiet declaration must not make the " +
			"interface erasable: typeOf and inferred.go would both hand out existential(d), " +
			"and Chooser<Int> and Chooser<String> are one pointer and therefore one kind and " +
			"one dispatch table")
	}
	if len(d.order) != 0 {
		t.Errorf("`Chooser` resolved %d method(s). resolveIfaces must still skip it: with no "+
			"resolved requirements nothing has been checked, and a minted table would be a "+
			"dispatch nobody verified", len(d.order))
	}
	var impl *implDef
	for _, id := range g.implOrder {
		if id.ifaceName == "Chooser" {
			impl = id
			break
		}
	}
	if impl == nil {
		t.Fatal("no `impl Chooser for Pair<E>` was registered at all, so this row cannot tell " +
			"a refused impl from an absent one")
	}
	if impl.lowerable {
		t.Error("`impl Chooser for Pair<E>` is lowerable. registerImplDef must keep refusing an " +
			"impl of an interface whose requirements were never resolved")
	}
	if impl.why != "generic interface" {
		t.Errorf("the impl block is refused as %q; want the interface's own reason", impl.why)
	}
}
