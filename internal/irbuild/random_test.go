package irbuild

import (
	"testing"
)

// TestGenerator_FieldKindAnswersAtTheNilGenBoundary checks that a
// `stdGenStructSpecs` field whose kind is a FUNCTION answers at the nil-gen
// boundary, as every other field kind does. It is its own test because the
// failure it guards is a PANIC rather than a refusal: a structural kind
// constructor that dereferences its gen crashes there. tupleKind, listKindIn
// and mapKindIn route their gen access through internComp, which handles a nil
// gen by interning process-wide when every component is package-neutral.
//
// The negative half is what makes this more than a smoke test: with a
// NON-NEUTRAL type argument and no gen there is no table to intern in, and the
// row must DECLINE rather than panic — the `Range` row's `Maybe<T>` field
// honours its `!shared` the same way.
func TestGenerator_FieldKindAnswersAtTheNilGenBoundary(t *testing.T) {
	if k := generatorFieldKind(nil, kindInt); k == kindInvalid {
		t.Error("Generator<Int>'s field has no kind at the nil-gen boundary")
	}
	// A def belonging to one generated package — the non-neutral case.
	local := kind{tag: tagNamed, def: &typeDef{nomi: "Probe", lowerable: true}}
	if local.packageNeutral() {
		t.Fatal("the probe kind is package-neutral, so this half tests nothing")
	}
	if k := generatorFieldKind(nil, local); k != kindInvalid {
		t.Errorf("Generator<Probe> answered %q at the nil-gen boundary; a "+
			"non-neutral argument has no shared instance and must decline",
			k.key())
	}
}

// TestStdSignatureCanNameATuple is the reachability witness for a tuple in a
// std signature. Its subjects are `std/random`'s two draw externs, whose
// signatures return a tuple, which projects to a shared tuple kind; were it
// refused, they would be `stdlib function outside the scalar subset`, a
// claim that a REPRESENTATION is missing. The test asserts that refusal is
// absent, and gives a positive and a negative for the shared tuple kind.
func TestStdSignatureCanNameATuple(t *testing.T) {
	idx := stdlibLowering()
	for _, name := range []string{"random.between_state", "random.unit_float_state"} {
		f, known := idx.byKey[name]
		if !known {
			t.Fatalf("%s is not in the stdlib index at all, so this witness "+
				"measures nothing", name)
		}
		if f.why == "stdlib function outside the scalar subset" {
			t.Errorf("%s refuses %q, so a `(Int, Int)` result did not project "+
				"to a shared kind", name, f.why)
		}
	}
	// THE POSITIVE, so a vacuous pass is impossible: a tuple over a std
	// opaque type has a shared kind.
	seed := randomSeedKind()
	if seed == kindInvalid {
		t.Fatal("std/random.Seed has no opaque row, so the tuple below has no " +
			"non-scalar component and the test is weaker than it claims")
	}
	k, shared := sharedTupleKind([]kind{kindInt, seed})
	if !shared || k == kindInvalid {
		t.Fatal("(Int, Seed) has no shared tuple kind")
	}
	if k.nomi() != "(Int, Seed)" {
		t.Errorf("(Int, Seed) is spelled %q", k.nomi())
	}
	// A tuple whose component this boundary may NOT admit must refuse, or a
	// package-relative kind would reach a stdlib signature.
	local := kind{tag: tagNamed, def: &typeDef{nomi: "Probe", lowerable: true}}
	if _, badShared := sharedTupleKind([]kind{kindInt, local}); badShared {
		t.Error("a tuple over a package-relative component was admitted at the " +
			"stdlib signature boundary; that kind is one no call site in another " +
			"gen could match")
	}
}
