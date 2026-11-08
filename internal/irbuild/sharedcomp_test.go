package irbuild

import (
	"testing"
)

// Process-wide structural interning has two properties:
//
//   - TestStructuralKindIdentityIsProcessWide: a structural kind's identity is
//     its interned *compKind, and a stdFunc's parameter and result kinds are
//     built once and compared BY POINTER against a call site's argument kinds
//     in a different gen. Two gens' `List<String>` must be one kind, or no
//     `List<...>` can cross the stdlib boundary.
//   - TestStructuralInterningDoesNotCollapseNominallyDistinctTypes: nominal
//     identity is `(declaring file, name)` and never the name alone, so two
//     files may each declare a `Point` and they are DISTINCT types. A
//     process-wide table keyed on the rendering would collapse them into a
//     wrong Debug impl, a wrong dispatch slot, or a wrong `==`.

// kindProbeGen is a gen with nothing but what a kind constructor touches: the
// intern table, and the usesRT flag seqKind and funcKind set. Deliberately not
// newGen, so the pin is about identity and not about a gen's other state.
func kindProbeGen() *gen { return &gen{} }

// TestStructuralInterningDoesNotCollapseNominallyDistinctTypes builds the
// collapse in the worst shape available and asserts it does not happen.
//
// Nominal identity is `(declaring file, name)`. Two generated packages each
// declaring a `Point` therefore hold two distinct `*typeDef`s, and the
// program-wide name registry (foreign.go's typeRegistry.names) normally gives
// the second a suffix so one package can name both. This pin does NOT lean on
// that: it builds two defs that render to the SAME Go identifier, which is the
// case a text-keyed process-wide table would collapse. `List<Point>` must still
// be two kinds, because a def belonging to a generated package is not
// package-neutral and must never enter a shared table at all.
//
// The identically-shaped FIELD SET is the point. The existing identity fixture's
// two Points have different fields, so a mix-up there is caught twice over; here
// the two are indistinguishable by shape and only identity separates them.
//
// # What this guard cannot see
//
// Every component it builds is a `*typeDef`, whose spelling the program-wide
// name registry keeps distinct. It never builds an interface existential, the
// one component kind whose spelling is shared BY CONSTRUCTION (`rt.Dyn`), and
// it separates its two Points into two GENS, so it cannot see a collision
// inside ONE gen. erased_test.go covers that case.
func TestStructuralInterningDoesNotCollapseNominallyDistinctTypes(t *testing.T) {
	// Two declarations, one spelling, one rendered Go name, identical shape.
	mk := func() *typeDef {
		return &typeDef{nomi: "Point", lowerable: true, fields: []fieldDef{{nomi: "x", k: kindInt}}}
	}
	d1, d2 := mk(), mk()
	if d1 == d2 {
		t.Fatal("probe is broken: the two defs must be two pointers")
	}

	g1, g2 := kindProbeGen(), kindProbeGen()
	if a, b := named(d1), named(d2); a == b {
		t.Fatal("two declarations of Point are one kind; nominal identity is not the declaration")
	}
	l1, l2 := g1.listKind(named(d1)), g2.listKind(named(d2))
	if l1 == l2 {
		t.Errorf("List<Point> over two DIFFERENT Points is one kind (%s): a generated package's type "+
			"reached a process-wide table, so one module's Point now stands for the other's", l1.nomi())
	}
	if l1.comp.parts[0].def != d1 || l2.comp.parts[0].def != d2 {
		t.Errorf("the interned element is not the def it was built from: %p/%p want %p/%p",
			l1.comp.parts[0].def, l2.comp.parts[0].def, d1, d2)
	}

	// The same claim one level in, because the collapse is worst where nothing
	// reads the element back: a tuple's part and a map's value.
	if a, b := g1.tupleKind([]kind{kindInt, named(d1)}), g2.tupleKind([]kind{kindInt, named(d2)}); a == b {
		t.Errorf("(Int, Point) over two different Points is one kind (%s)", a.nomi())
	}
	if a, b := g1.mapKind(kindString, named(d1)), g2.mapKind(kindString, named(d2)); a == b {
		t.Errorf("Map<String, Point> over two different Points is one kind (%s)", a.nomi())
	}

	// A tid is recorded per KIND, so two same-named defs get two identities.
	g := kindProbeGen()
	g.tids = map[kind]bool{}
	g.hasTID(named(d1))
	g.hasTID(named(d2))
	if len(g.tids) != 2 {
		t.Errorf("two declarations of Point hold %d runtime identities, want 2", len(g.tids))
	}
}
