package irbuild

import (
	"testing"
)

// The pinned collection programs. Both assert the reference TEXT as well as
// agreement, because a differential comparison passes when both sides silently
// produce nothing.

// TestTupleIsOneGoTypePerShape pins the property the anonymous-struct
// representation buys and a generated named type would have had to reconstruct:
// two spellings of one tuple shape are ONE kind, compared with plain `==`.
//
// The pointer is the identity (see composite.go), so this is a pointer-equality
// assertion in disguise — which is the point. If two `(Int, String)` kinds were
// two pointers, every type agreement check in the builder would refuse valid
// programs, and the enum slot layout would allocate two fields for one type.
func TestTupleIsOneGoTypePerShape(t *testing.T) {
	g := &gen{types: map[string]*typeDef{}}
	a := g.tupleKind([]kind{kindInt, kindString})
	b := g.tupleKind([]kind{kindInt, kindString})
	c := g.tupleKind([]kind{kindString, kindInt})
	if a != b {
		t.Fatalf("two (Int, String) kinds are not equal: %v vs %v", a.nomi(), b.nomi())
	}
	if a == c {
		t.Fatalf("(Int, String) and (String, Int) share one kind: %v", a.nomi())
	}
	if a.nomi() != "(Int, String)" {
		t.Fatalf("tuple spelling is %q", a.nomi())
	}
	// Arity below two is a caller bug, not a one-field struct: Nomi has no
	// 1-tuple, so `(x)` is a grouped expression and must never reach here as a
	// shape.
	if k := g.tupleKind([]kind{kindInt}); k != kindInvalid {
		t.Fatalf("a 1-tuple produced %q instead of kindInvalid", k.key())
	}
}

// TestListElementIsPartOfTheGoType is the list half of the above, and the case
// a bare tag would get wrong: one Nomi type constructor, unboundedly many Go
// types.
func TestListElementIsPartOfTheGoType(t *testing.T) {
	g := &gen{types: map[string]*typeDef{}}
	ints := g.listKind(kindInt)
	if ints != g.listKind(kindInt) {
		t.Fatal("two List<Int> kinds are not equal")
	}
	if ints == g.listKind(kindString) {
		t.Fatal("List<Int> and List<String> share one kind")
	}
	if ints == g.listKind(ints) {
		t.Fatal("List<Int> and List<List<Int>> share one kind")
	}
	if got := g.listKind(ints).nomi(); got != "List<List<Int>>" {
		t.Fatalf("List<List<Int>> lowered to %q", got)
	}
	// An unrepresentable element makes the whole list unrepresentable, rather
	// than a list of something unnamed.
	if k := g.listKind(kindInvalid); k != kindInvalid {
		t.Fatalf("List<?> produced %q instead of kindInvalid", k.key())
	}
	// The empty list is NOT a List<Unit> in disguise, however alike their Go
	// types look: nothing may mistake one for a real list of anything.
	if kindEmptyList == g.listKind(kindUnit) {
		t.Fatal("the untyped empty list shares a kind with List<Unit>")
	}
}
