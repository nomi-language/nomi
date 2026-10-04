package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
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

// TestTupleSigKind_AStdSignatureCanNameATuple is the reachability witness for
// `stdTypeKind`'s tuple arm. Its subjects are `std/random`'s two draw externs,
// whose signatures return a tuple. Without the arm they would refuse `stdlib
// function outside the scalar subset`, a claim that a REPRESENTATION is
// missing; with it the signature is admitted, so any refusal left is a more
// specific one such as `stdlib host function`. The test asserts that the
// signature refusal is absent, and gives a positive and a negative for the
// arm itself.
func TestTupleSigKind_AStdSignatureCanNameATuple(t *testing.T) {
	idx := stdlibLowering()
	for _, name := range []string{"random.below_state", "random.unit_float_state"} {
		f, known := idx.byKey[name]
		if !known {
			t.Fatalf("%s is not in the stdlib index at all, so this witness "+
				"measures nothing", name)
		}
		if f.why == "stdlib function outside the scalar subset" {
			t.Errorf("%s refuses %q, so the tuple arm did not admit a "+
				"`(Int, Int)` result", name, f.why)
		}
	}
	// THE POSITIVE, so a vacuous pass is impossible: the arm answers a real kind
	// for a real spelling, through the same restricted resolver std uses.
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
	// A tuple whose component this boundary may NOT admit must refuse, or the
	// arm would smuggle a package-relative kind into a stdlib signature.
	local := kind{tag: tagNamed, def: &typeDef{nomi: "Probe", lowerable: true}}
	if _, badShared := sharedTupleKind([]kind{kindInt, local}); badShared {
		t.Error("a tuple over a package-relative component was admitted at the " +
			"stdlib signature boundary; that kind is one no call site in another " +
			"gen could match")
	}
}

// TestTupleSigKind_AnArrowIsNotATuple pins the one-line distinction the arm
// rests on, because the parser spells both shapes with the SAME node.
//
// `(Int, String)` and `(Int) -> String` are both `*ast.FuncType`; the arrow is
// the only difference (a nil `Return` vs a set one). An arm that ignored that
// would admit a FUNCTION type into any stdlib signature position, which has no
// customer today and would be a projection with no reachable site.
func TestTupleSigKind_AnArrowIsNotATuple(t *testing.T) {
	anchors := stdAnchors{}
	arrow := &ast.FuncType{
		Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int"}},
		Return: &ast.SimpleType{Name: "String"},
	}
	if _, handled := tupleSigKind(arrow, anchors); handled {
		t.Error("`(Int) -> String` was handled as a tuple; the arrow makes it a " +
			"function type and this arm must decline so other arms see it")
	}
	// A 1-tuple does not exist in Nomi: `(T)` is a grouped type, and declining
	// is what lets the grouped inner type reach the caller's other arms.
	grouped := &ast.FuncType{Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int"}}}
	if _, handled := tupleSigKind(grouped, anchors); handled {
		t.Error("`(Int)` was handled as a tuple; Nomi has no 1-tuple and this " +
			"spelling is a grouped type")
	}
	// The positive, so the two negatives are not the whole of what is measured.
	pair := &ast.FuncType{Params: []ast.TypeExpr{
		&ast.SimpleType{Name: "Int"}, &ast.SimpleType{Name: "Int"},
	}}
	k, handled := tupleSigKind(pair, anchors)
	if !handled || k == kindInvalid {
		t.Fatalf("`(Int, Int)` was not handled as a tuple (handled=%v)", handled)
	}
}
