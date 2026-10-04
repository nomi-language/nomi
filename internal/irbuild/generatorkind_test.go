package irbuild

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestIRGeneratorFieldKindIsPackageNeutral holds that std/random's
// `Generator<T>` field kind, a function `(Seed) -> (T, Seed)`, is
// package-neutral when `T` is, so a Generator instance over it can be interned
// process-wide (random.go checks `packageNeutral` on the argument).
// `packageNeutral`'s structural arm covers `tagFunc` and `tagTuple` and recurses
// into their components, so the answer follows from `Seed` being an
// rt-declared opaque type.
func TestIRGeneratorFieldKindIsPackageNeutral(t *testing.T) {
	g := &gen{types: map[string]*typeDef{}}
	seed := opaqueKindOfGoType(reflect.TypeFor[rt.Seed]())
	if seed.tag != tagNamed {
		t.Fatalf("std/random.Seed is not an anchored opaque newtype (tag %v)", seed.tag)
	}
	if !seed.packageNeutral() {
		t.Fatal("Seed is not package-neutral, so nothing built over it can be shared")
	}
	res := g.tupleKind([]kind{kindInt, seed})
	if !res.packageNeutral() {
		t.Fatal("(Int, Seed) is not package-neutral, so the field kind cannot be shared")
	}
	fk := g.funcKind([]kind{seed}, res)
	if !fk.packageNeutral() {
		t.Fatalf("(Seed) -> (Int, Seed) is not package-neutral, so a Generator<Int> "+
			"instance cannot be shared across programs: %s", fk.key())
	}
}
