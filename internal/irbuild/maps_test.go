package irbuild

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestMapKeyOpsAgreeOnTheirDomain holds valueHash and valueEqual in step.
//
// A hash without a matching equality is a bucket nobody can search, and a
// widening of one without the other is not a clean refusal: mapOps takes both,
// so the pair still refuses, but under a name that blames the wrong half. The
// real risk is a change adding a case to one function and not the other, so
// this walks every kind the builder can build and asserts the two answers
// match.
//
// A pairwise-agreement guard is only as wide as the shapes it enumerates BY
// HAND. So the rule for anyone extending either function is: add the shape
// here in the same change, or this guard stays green while the pair diverges.
// The Decimal row is below, beside the two rt-opaque leaves it must NOT be
// confused with.
func TestMapKeyOpsAgreeOnTheirDomain(t *testing.T) {
	g := &gen{}
	shapes := []struct {
		name string
		k    kind
	}{
		{"Int", kindInt},
		{"Float", kindFloat},
		{"String", kindString},
		{"Bool", kindBool},
		{"Unit", kindUnit},
		{"List<_>", kindEmptyList},
		{"List<Int>", g.listKind(kindInt)},
		{"List<List<String>>", g.listKind(g.listKind(kindString))},
		{"(Int, String)", g.tupleKind([]kind{kindInt, kindString})},
		{"(Int, List<Bool>)", g.tupleKind([]kind{kindInt, g.listKind(kindBool)})},
		{"Map<String, Int>", g.mapKind(kindString, kindInt)},
		{"Map<List<Int>, (Int, Int)>", g.mapKind(g.listKind(kindInt),
			g.tupleKind([]kind{kindInt, kindInt}))},
		{"a struct", named(&typeDef{nomi: "Point", lowerable: true, fields: []fieldDef{
			{nomi: "x", k: kindInt},
			{nomi: "y", k: kindString},
		}})},
		{"an enum", named(&typeDef{nomi: "Shape", isEnum: true, lowerable: true, variants: []variantDef{{nomi: "Dot", tag: 1, kind: "bare"}}})},
		{"a distinct", named(&typeDef{nomi: "Meters", isDistinct: true, inner: kindInt, lowerable: true})},
		{"a marker", named(&typeDef{nomi: "Expired", isDistinct: true, inner: kindInvalid, lowerable: true})},
		// THE ROW WHOSE ABSENCE MADE THIS GUARD BLIND. A Decimal is a
		// `tagNamed` kind like the three above it and unlike any of them it is
		// `rtOpaque`, so it takes neither the struct/enum/distinct path nor the
		// scalar tags — which is exactly why no other row stood in for it.
		{"Decimal", decimalKind()},
		// The two OTHER rt-opaque leaves, present so that a future widening of
		// the Decimal arm to "any rtOpaque kind" is visible here. Agreement is
		// all this loop checks and both of these agree by DECLINING, so the
		// answers themselves are pinned in TestMapKeyOpsAdmitDecimalAndRefuseOpaqueLeaves.
		{"Byte", stdHostKindOfGoType(reflect.TypeFor[rt.Byte]())},
		{"Bytes", stdHostKindOfGoType(reflect.TypeFor[rt.Bytes]())},
		// The two the builder has no structural identity for. Both must be
		// absent from BOTH functions.
		{"a function type", g.funcKind([]kind{kindInt}, kindInt)},
		{"an interface existential", existential(&ifaceDef{nomi: "Speaker", lowerable: true})},
	}
	for _, s := range shapes {
		hashOK := g.valueHashes(s.k)
		eqOK := g.valueEquates(s.k)
		if hashOK != eqOK {
			t.Errorf("%s: valueHash ok=%v but valueEqual ok=%v — a map key needs both, "+
				"and widening one without the other misreports which half is missing",
				s.name, hashOK, eqOK)
		}
	}
}

// TestMapKeyOpsAdmitDecimalAndRefuseOpaqueLeaves pins the ANSWERS where the
// agreement loop above only pins the pair.
//
// Both functions declining is agreement, so the loop cannot tell "Decimal is a
// keyable value" from "Decimal is not". It has to, because the criterion that
// separates an admitted rt leaf from a refused one is easy to state wrongly.
// They are all `rtOpaque` leaves; what admits one is NOT that this builder may
// look inside it (it may not) but that rt.Hash and rt.Equal literally CALL rt's
// own function for that type, so the built hasher is the structural hash
// itself rather than a second implementation of it.
//
// `Byte` AND `Bytes` ARE ON THE ADMITTED SIDE by that criterion: `rt.HashByte`
// and `rt.HashBytes` exist and rt.Hash's two cases call them, exactly as its
// Decimal case calls `rt.HashDecimal`. That is why the assertion is over the
// answer rather than over the `rtOpaque` flag it would be tempting to read.
//
// The equality half moves with the hash: equality without a matching hash is
// a wrong BUCKET rather than a wrong answer, so the pair moves together or not
// at all.
//
// The REFUSING rows are what keep this from being a one-sided claim, and they are
// leaves for which rt makes no hashing claim at all: `Supervisor` and `Regex` are
// host handles with no structural contents, so a widening of the admitted arm to
// `rtOpaque` in general would silently take both.
func TestMapKeyOpsAdmitDecimalAndRefuseOpaqueLeaves(t *testing.T) {
	g := &gen{}
	for _, c := range []struct {
		name string
		k    kind
		want bool
	}{
		{"Decimal", decimalKind(), true},
		{"Byte", stdHostKindOfGoType(reflect.TypeFor[rt.Byte]()), true},
		{"Bytes", stdHostKindOfGoType(reflect.TypeFor[rt.Bytes]()), true},
		{"Supervisor", stdHostKindOfGoType(reflect.TypeFor[rt.Supervisor]()), false},
		{"Regex", stdHostKindOfGoType(reflect.TypeFor[rt.Regex]()), false},
	} {
		if c.k == kindInvalid {
			t.Fatalf("%s has no stdHostSpecs row, so its row here names nothing", c.name)
		}
		hashOK := g.valueHashes(c.k)
		eqOK := g.valueEquates(c.k)
		if hashOK != c.want || eqOK != c.want {
			t.Errorf("%s: valueHash ok=%v valueEqual ok=%v, want both %v — the criterion is "+
				"whether value.Hash/value.Equal call rt's own function for this type, not whether "+
				"the type is an rt-opaque leaf", c.name, hashOK, eqOK, c.want)
		}
	}
}

// TestMapKeyOpsRefuseAFunctionKey pins the boundary from the other side: a
// function is not a keyable value at all, so the
// refusal has to be present rather than assumed absent — an "agree on their
// domain" test passes trivially if the domain is everything.
func TestMapKeyOpsRefuseAFunctionKey(t *testing.T) {
	g := &gen{}
	fn := g.funcKind([]kind{kindInt}, kindInt)
	if ok := g.valueHashes(fn); ok {
		t.Error("valueHash accepted a function type; a function is not a keyable value")
	}
	if ok := g.valueEquates(fn); ok {
		t.Error("valueEqual accepted a function type; value.Equal has no answer for one")
	}
}
