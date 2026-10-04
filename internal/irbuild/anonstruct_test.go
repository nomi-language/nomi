package irbuild

import (
	"slices"
	"testing"
)

// TestAnonStructIdentityIsTheFieldSet is the intern-key assertion for
// anonymous records. Intern tables key on the rendered type, and a structural
// type keyed on a rendered string alone collides.
//
// Four claims, each a different way the key could be wrong:
//
//   - Two field ORDERINGS of one record are ONE kind. This is what the
//     canonical sort buys, and without it `{x: 1, y: 2}` and `{y: 2, x: 1}`
//     would be two Go types and neither assignable to the other.
//   - Two records with the same COMPONENT KINDS and different NAMES are TWO
//     kinds. `{a: Int, b: Int}` and `{x: Int, y: Int}` have identical `parts`,
//     so the separation cannot come from the components alone.
//   - A record and a NOMINAL struct of identical shape are two kinds. Nominal
//     identity is the *typeDef pointer and a record's is the *compKind, so
//     they cannot be equal whatever they render as.
//   - A record over two different EXISTENTIALS is two kinds. Both render
//     `struct { F_v rt.Dyn }`, so the rendered text alone cannot separate them;
//     the `parts` half of the key does.
func TestAnonStructIdentityIsTheFieldSet(t *testing.T) {
	g := kindProbeGen()
	forward := g.anonStructKind([]string{"x", "y"}, []kind{kindInt, kindString})
	backward := g.anonStructKind([]string{"y", "x"}, []kind{kindString, kindInt})
	if forward == kindInvalid {
		t.Fatal("a record of Int and String has no kind")
	}
	if forward != backward {
		t.Errorf("two orderings of one record are two kinds:\n  %s\n  %s",
			forward.nomi(), backward.nomi())
	}
	if got := forward.nomi(); got != "{x: Int, y: String}" {
		t.Errorf("canonical Nomi spelling = %q", got)
	}

	ab := g.anonStructKind([]string{"a", "b"}, []kind{kindInt, kindInt})
	xy := g.anonStructKind([]string{"x", "y"}, []kind{kindInt, kindInt})
	if ab == xy {
		t.Error("two records with the same component kinds and different field names collapsed into one kind")
	}
	if !slices.Equal(ab.comp.parts, xy.comp.parts) {
		t.Fatal("the witness is wrong: the two records must have IDENTICAL parts for this to test the names")
	}

	nominal := named(&typeDef{nomi: "Point", lowerable: true})
	shaped := g.anonStructKind([]string{"x", "y"}, []kind{kindInt, kindInt})
	if nominal == shaped {
		t.Error("a record unified with a nominal struct of the same shape")
	}
	if nominal.nomi() == shaped.nomi() {
		t.Error("a record and a nominal struct render to one Go type; Go would then unify them too")
	}

	alpha := existential(&ifaceDef{nomi: "Alpha"})
	beta := existential(&ifaceDef{nomi: "Beta"})
	ea := g.anonStructKind([]string{"v"}, []kind{alpha})
	eb := g.anonStructKind([]string{"v"}, []kind{beta})
	if ea == eb {
		t.Error("two records over different existentials collapsed; the rendered text alone was taken as the key")
	}
}
