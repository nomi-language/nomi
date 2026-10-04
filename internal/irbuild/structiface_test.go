package irbuild

import (
	"testing"
)

// TestStructIface_TheStdDeclarationStillHasTheShapeThisFileAssumes is
// structiface.go's EXPIRY condition as a test, asserted against the real
// `std/structs.nomi` rather than against a reading of it.
//
// The claim it defends is the one the whole file rests on: the def carries ZERO
// methods because std declares exactly ONE and that one is not dispatchable
// (`host fn update(original: self, updates: Partial<self>): self` — self in the
// return plus a compiler-known type operator). A std edit that added a
// DISPATCHABLE method would leave the empty def silently wrong, and no
// differential fixture could see it: `Struct.foo(erased)` would refuse for want
// of a method rather than dispatch, which reads like a coverage gap.
//
// POSITIVE, so a run that measured nothing fails: it asserts the anchor EXISTS
// for a module naming the prelude's Struct, which is the same predicate the builder
// uses, and then separately that a shape edit REMOVES it.
func TestStructIface_TheStdDeclarationStillHasTheShapeThisFileAssumes(t *testing.T) {
	p, err := AnalyzeSource("j3struct", `

struct Point {
  x: Int
}

fn one(_s: Struct): Int {
  1
}

test "anchored" {
  assert one(Point{x: 1}) == 1
}
`)
	if err != nil {
		t.Fatalf("the fixture does not analyze, so no anchor was measured: %v", err)
	}
	if len(p.Modules) == 0 || p.Modules[0].FA == nil {
		t.Fatal("no FileAnalysis, so structIfaceAnchored had nothing to resolve against")
	}
	if !structIfaceAnchored(p.Modules[0].FA) {
		t.Fatal("std's `interface Struct` NO LONGER has the shape structiface.go assumes, so every " +
			"`Struct` position now refuses under `stdlib interface`.\n\n" +
			"structIfaceMatches requires: public, non-generic, no fields, no variants, no attached " +
			"tests, and exactly ONE method — `update`, EXTERN, no type parameters, parameters " +
			"`original: self` and `updates: Partial<self>` with no defaults or destructuring, " +
			"returning `self`.\n\n" +
			"If std gained a DISPATCHABLE method, structiface.go's central claim (the def carries no " +
			"methods) is wrong and the def needs one. If the shape merely moved, update " +
			"structIfaceMatches. Do NOT relax the check: it is the only thing that turns a std shape " +
			"drift into a loud refusal instead of a silent non-dispatch")
	}
}

// TestStructIface_ANonStructIsNotErasedIntoTheMarker pins the language rule the
// universal predicate implements, in the direction that would be a WRONG ANSWER
// rather than a missing feature.
//
// Spec: "no non-struct type satisfies it", and
// `11-interfaces-and-impls/structural_interfaces` pins the front-end half
// ("Int does not implement Struct"). An enum and a distinct are the two named
// types that must answer NO, and a `true` for either would be this builder
// disagreeing with the checker rather than being generous.
//
// Asserted on the PREDICATE rather than through a program, because the front end
// rejects the program: `one(5)` is a front-end error, so a program-level test
// would measure the checker and not this file. Stated so nobody reads it as a
// weaker test than it is — the predicate IS what `bindsImpl` consults.
func TestStructIface_ANonStructIsNotErasedIntoTheMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    kind
		want bool
	}{
		{"Int", kindInt, false},
		{"Float", kindFloat, false},
		{"String", kindString, false},
		{"Bool", kindBool, false},
		{"Unit", kindUnit, false},
		{"a named struct", named(&typeDef{nomi: "Point"}), true},
		{"an enum", named(&typeDef{nomi: "Color", isEnum: true}), false},
		{"a distinct", named(&typeDef{nomi: "Meters", isDistinct: true}), false},
		{"an existential", existential(structIfaceDef()), false},
		{"a nil-def named kind", kind{tag: tagNamed}, false},
	} {
		if got := structIfaceSatisfiedBy(tc.k); got != tc.want {
			t.Errorf("structIfaceSatisfiedBy(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The record half, built through the real interning path rather than by
	// hand, because `tagAnonStruct` without a `comp` is not a record.
	g := &gen{comps: map[string][]*compKind{}}
	rec := g.anonStructKind([]string{"tag"}, []kind{kindString})
	if !structIfaceSatisfiedBy(rec) {
		t.Errorf("structIfaceSatisfiedBy(an anonymous struct) = false, want true: the spec says "+
			"\"a named `struct` type or an anonymous `{...}`\" satisfies Struct, and "+
			"11/structural_interfaces writes `{tag: \"anon\"}` into a `List<Struct>` (kind %v)", rec.tag)
	}
}
