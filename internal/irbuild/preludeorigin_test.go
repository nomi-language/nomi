package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestPreludeOrigin_ShapeIsEVALUATEDAgainstStdsOwnDeclaration is the guard on
// the difference between citing a precondition and testing it.
//
// The anchor's claim is "std really declares this enum, in this shape". A
// version that merely CITED the spec table would authorize a layout from a
// table nobody checked, and at `Fragment<String>` — the only instantiation the
// corpus reaches — a swapped-payload spec is invisible.
//
// Both halves are asserted, because only the pair is a test: the real spec must
// VALIDATE (or the whole route is dead and every other assertion here is
// vacuous), and a deliberately wrong one must NOT.
func TestPreludeOrigin_ShapeIsEvaluatedAgainstStdsOwnDeclaration(t *testing.T) {
	lib := std.Load()
	if fragmentSpec == nil {
		t.Fatal("no std/literals.Fragment spec to validate")
	}
	if !stdDeclaresSpec(lib, fragmentSpec) {
		t.Fatal("std's own declaration does not match the Fragment spec; " +
			"the origin route is dead and every other assertion in this file is vacuous")
	}

	// The payloads SWAPPED. `Static` is the concrete `String` and `Dynamic` is
	// the type parameter, and this says the reverse. It is the exact mistake
	// `matches` was written for, and the one no `Fragment<String>` program can
	// see.
	swapped := *fragmentSpec
	swapped.variants = []preludeVariantSpec{
		{nomi: "Static", param: 0, field: "Static"},
		{nomi: "Dynamic", param: -1, fixed: kindString, field: "Dynamic"},
	}
	if stdDeclaresSpec(lib, &swapped) {
		t.Error("a spec with Fragment's payloads swapped validated against std's declaration")
	}

	// The variants REORDERED, which permutes the tags every `case` switches on.
	reordered := *fragmentSpec
	reordered.variants = []preludeVariantSpec{
		fragmentSpec.variants[1],
		fragmentSpec.variants[0],
	}
	if stdDeclaresSpec(lib, &reordered) {
		t.Error("a spec with Fragment's variants reordered validated against std's declaration")
	}

	// A module std does not have, and a name that module does not declare.
	elsewhere := *fragmentSpec
	elsewhere.origin = "std/toml"
	if stdDeclaresSpec(lib, &elsewhere) {
		t.Error("the Fragment spec validated against std/toml, which declares no Fragment")
	}
}

// TestPreludeOrigin_IdentityIsTheTypesOwnOriginAndNeverItsName pins the half a
// name-keyed table gets wrong.
//
// A user CAN declare `enum Fragment<T>`; the analyzer gives it that user
// module's Origin, and `std` is unforgeable as a module name. So the negative
// cases are the test, and they are asserted at the unit because the positive
// one already has a program (TestPinned_PreludeOriginSibling).
func TestPreludeOrigin_IdentityIsTheTypesOwnOriginAndNeverItsName(t *testing.T) {
	if _, ok := preludeOriginAnchor("std/literals", "Fragment"); !ok {
		t.Fatal("std/literals.Fragment did not anchor; the denominator is empty")
	}
	for _, tc := range []struct{ origin, name string }{
		{"myapp/literals", "Fragment"},
		{"myapp", "Maybe"},
		{"", "Fragment"},
		{"std/literals", "Maybe"},
		{"std/literals", "NotAThing"},
	} {
		if _, ok := preludeOriginAnchor(tc.origin, tc.name); ok {
			t.Errorf("(%q, %q) anchored to a prelude spec", tc.origin, tc.name)
		}
	}
}
