package irbuild

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestStdHostSpecNamesMatchTheirSingleton is the pairing guard.
//
// `nomi` is stated on the row rather than derived from `prim`, because
// stdHostAnchors uses it to look the name up in module scope and deriving it
// from the singleton it is about to confirm would make that check circular. So
// the two can drift, and this is what stops them: a row whose spelling is not
// its singleton's would anchor the WRONG name — silently, because a name that
// resolves to nothing simply produces no anchor and every mention refuses,
// which reads exactly like the feature not being implemented.
//
// An ORIGIN row has no singleton to pair with, so what is checkable there is
// only the rt type. Its identity is checked instead by
// TestStdHostSpecsIdentifyExactlyOneWay and, from the outside, by
// TestStdHostContextShadowDoesNotAnchor.
func TestStdHostSpecNamesMatchTheirSingleton(t *testing.T) {
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		if s.prim != nil {
			if got := s.prim.String(); got != s.nomi {
				t.Errorf("stdHostSpecs[%d] spells its type %q; its singleton is %q", i, s.nomi, got)
			}
		}
		if s.goType == nil || !rtNamed(s.goType) {
			t.Errorf("stdHostSpecs[%d] (%s) has no resolvable rt type", i, s.nomi)
		}
	}
}

// TestStdHostSpecsIdentifyExactlyOneWay is the invariant stdHostAnchors' switch
// is written against, and the failure it prevents is specific: a row with
// NEITHER field set would fall to that switch's `default` and anchor nothing,
// which reads as the type not being implemented; a row with BOTH would make the
// singleton branch win silently and the origin check dead, so a later std edit
// that moved the declaration would keep anchoring.
//
// Two fields rather than an enum tag because each carries the identity it names
// — the same reason opaqueSpec holds `origin` and `nomi` rather than a composed
// key — and this is the guard that makes the pair exclusive rather than merely
// documented.
func TestStdHostSpecsIdentifyExactlyOneWay(t *testing.T) {
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		switch {
		case s.prim != nil && s.origin != "":
			t.Errorf("stdHostSpecs[%d] (%s) sets BOTH prim and origin; the singleton branch would "+
				"win and the origin check would be dead code", i, s.nomi)
		case s.prim == nil && s.origin == "":
			t.Errorf("stdHostSpecs[%d] (%s) sets NEITHER prim nor origin, so it identifies nothing "+
				"and anchors nowhere — every mention of the type refuses", i, s.nomi)
		}
	}
}

// TestStdHostDefsAreDistinctAndPackageNeutral is the precondition the
// process-wide sharing rests on, asserted rather than commented.
//
// foreign.go forbids a shared def in general, and its reason is that "a shared
// def's field kinds are interned in the OWNER's g.comps", so a component
// renders correctly in one package and nowhere else. These defs are exempt
// because they have NO components at all — no inner, no fields, no variants —
// and their goName is an `rt.` spelling that means the same thing in every
// gen.
//
// That exemption is only true while the defs stay LEAVES, so this asserts it.
// A future row over a type with a payload this builder can see would break the
// property silently: the def would still work in the declaring gen and carry a
// wrong kind in every other one.
func TestStdHostDefsAreDistinctAndPackageNeutral(t *testing.T) {
	defs := stdHostDefs()
	if len(defs) != len(stdHostSpecs) {
		t.Fatalf("%d defs for %d specs", len(defs), len(stdHostSpecs))
	}
	seen := map[string]int{}
	for i, d := range defs {
		if d.inner != kindInvalid || len(d.fields) > 0 || len(d.variants) > 0 {
			t.Errorf("%s is not a leaf: inner=%v fields=%d variants=%d — see the header",
				d.nomi, d.inner, len(d.fields), len(d.variants))
		}
		if !d.rtDeclared {
			t.Errorf("%s is not rtDeclared, so typeDecl would emit a SECOND Go type for it", d.nomi)
		}
		if !d.lowerable {
			t.Errorf("%s is not lowerable, so every mention refuses and the row buys nothing", d.nomi)
		}
		if prev, dup := seen[d.nomi]; dup {
			t.Errorf("stdHostSpecs[%d] and [%d] are both spelled %s",
				prev, i, d.nomi)
		}
		seen[d.nomi] = i
	}
}

// TestStdHostKindLookupsAgree checks the channels answer the same kind, and
// which channels a row HAS.
//
// A type reaches this builder three ways and each has its own lookup: an
// ANNOTATION (`fn f(b: Byte)`) through stdHostNamed, an INFERRED type
// (`|b| ...`) through stdHostKindOfPrimitive, and an rt SIGNATURE through
// stdHostKindOfGoType. Lookups that must agree is the shape a fourth
// silently-different answer hides in — `lambda_test.go` records exactly that
// hazard for Decimal ("`d: Decimal` and `|d|` over one must not be two rows").
// Pointer equality, because that is what stdSignatureMatches compares.
//
// AN ORIGIN ROW HAS TWO CHANNELS, NOT THREE, and this asserts the absence in
// both directions rather than skipping it. The inferred channel is where a
// solved type arrives with no scope to consult, so the only test available there
// would be on the NAME — and a user's own `pub host type Context` would then
// adopt rt's representation. `Context` is NOT a reserved name (it is not
// prelude-injected, so analysis' parent-scope reservation never reaches it —
// the same line that leaves `App` and `Assertable` shadowable), which is
// precisely why the singleton reasoning that makes the first three rows safe
// does NOT transfer. So the absence is the safe answer and it is pinned here:
// widening stdHostKindOfPrimitive to match by name fails this test.
func TestStdHostKindLookupsAgree(t *testing.T) {
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		want := stdHostKind(i)
		if got := stdHostKindOfGoType(s.goType); got != want {
			t.Errorf("%s: the rt SIGNATURE channel answers a different kind than the spec", s.nomi)
		}
		if s.prim == nil {
			// The negative half for an origin row. `&analysis.PrimitiveType{...}`
			// cannot be constructed here (the field is unexported), so the stand-in
			// is the row's own absence from the loop inside
			// stdHostKindOfPrimitive: passing a nil analysis.Type must answer
			// kindInvalid and never this row's kind.
			if got := stdHostKindOfPrimitive(nil); got == want {
				t.Errorf("%s: the INFERRED channel answered an ORIGIN row's kind; a solved type "+
					"carries only a name there, so matching on it would let a user's own "+
					"`pub host type %s` adopt rt's representation", s.nomi, s.nomi)
			}
			continue
		}
		if got := stdHostKindOfPrimitive(s.prim); got != want {
			t.Errorf("%s: the INFERRED channel answers a different kind than the spec", s.nomi)
		}
	}
	// The negative half, and it is the one a name-only or underlying-kind match
	// would fail: rt.Bytes IS a Go string, so a lookup keyed on the underlying
	// kind would answer `Bytes` for every `string` parameter in the registry
	// and bind `String.trim` to the wrong declaration.
	if k := stdHostKindOfGoType(reflect.TypeFor[string]()); k != kindInvalid {
		t.Error("a plain Go string projected onto an anchored host type; " +
			"the lookup is matching the UNDERLYING kind rather than the type")
	}
	// The other structurally negative row: a scalar is scalarKind's rather than
	// this table's.
	if k := stdHostKindOfPrimitive(analysis.TypeString); k != kindInvalid {
		t.Error("String answered a host kind; the scalars are scalarKind's, not this table's")
	}
}

// TestStdHostIdentityIsGuardedByTheFrontEndFirst records WHERE this family's
// identity actually comes from, because it is not where the other three get
// theirs.
//
// The other anchor families reconstruct identity from (Origin, Name) because
// two user files may each declare a `Duration`. The obvious analogue here would
// be a witness in which a user declares their own `Bytes` and the anchor
// declines it. THAT WITNESS CANNOT BE WRITTEN: the front end rejects the
// program outright with
//
//	type name 'Bytes' is reserved by the language and cannot be redeclared as
//	a struct; use a distinct type (`type MyBytes Bytes`) for a domain-specific
//	variant
//
// which is a strictly stronger guarantee than any check this builder could
// make, and it is the reason a singleton POINTER is sufficient identity here
// where a name would not be elsewhere. So stdHostAnchors' scope confirmation is
// belt-and-braces rather than the load-bearing check, and saying so is the
// point of this test: a reader who assumed it was load-bearing would price a
// change to it wrongly in either direction.
//
// What IS asserted: the reserved-name rule still holds (so the reasoning above
// stays true), and the family fails safely with no analysis.
func TestStdHostIdentityIsGuardedByTheFrontEndFirst(t *testing.T) {
	// No analysis at all: the state a module analyzed without a library is in,
	// and the state this family must fail safely from.
	if got := stdHostAnchors(nil); len(got) != 0 {
		t.Errorf("a nil analysis produced %d anchors; it must produce none", len(got))
	}
	for _, name := range []string{"Byte", "Bytes"} {
		src := "struct " + name + " {\n  n: Int\n}\n\nfn main() {\n  _ = " + name + "{n: 1}\n}\n"
		if _, err := AnalyzeSource("main", src); err == nil {
			t.Errorf("the front end ACCEPTED a user declaration of %q. This family's "+
				"identity rests on that being impossible — see the doc comment — so "+
				"stdHostAnchors' scope check is now load-bearing and needs the "+
				"(Origin, Name) treatment the other three families have.", name)
		}
	}
}

func TestStdHostInferredProjectionRequiresDeclaredIdentity(t *testing.T) {
	p, err := AnalyzeSource("main", `import std/dynamic.Dynamic
fn identity(value:Dynamic):Dynamic{value}`)
	if err != nil {
		t.Fatal(err)
	}
	fa := p.Modules[0].FA
	sym := fa.ModuleScope.Lookup("Dynamic")
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	g := &gen{fa: fa}
	if got := g.project(sym.Type); got != stdHostOriginKind("std/dynamic", "Dynamic") {
		t.Fatal("imported Dynamic lost its representation")
	}
	fake := &analysis.PrimitiveType{Name_: "Dynamic"}
	if got := g.project(fake); got != kindInvalid {
		t.Fatal("same spelling adopted another declaration's representation")
	}
	scope := analysis.NewScope(nil)
	scope.Define(&analysis.Symbol{Name: "Dynamic", Type: fake, Node: &ast.ExternType{Name: "Dynamic"}})
	g.fa = &analysis.FileAnalysis{Origin: "user/other", ModuleScope: scope}
	if got := g.project(fake); got != kindInvalid {
		t.Fatal("user declaration adopted stdlib representation")
	}
}
