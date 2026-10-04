package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The guards over `std/json.Json`, a SELF-REFERENTIAL type in the shared-def
// families.
//
// # WHAT EACH GUARD HERE CAN AND CANNOT SEE
//
// The reflection guards in stdenum_test.go already hold the LAYOUT against
// rt.Json in both directions (TestStdEnumSlotsMatchRT), the neutrality
// precondition per slot (TestStdEnumDefsArePackageNeutral) and the spec against
// std's own declaration order (TestStdEnumShapeMatchesStdSource). None of them
// builds a value, so none can see whether a `case` arm reads the field its own
// variant stores into, whether a Map payload survives the boundary in insertion
// order, or whether an emitted value resolves to std's own `impl Display for
// Json`. That is what the differential and the pinned text below are for.
//
// # WHICH GUARD CATCHES WHICH DEFECT
//
//  1. `Int` and `Float` swapped in the spec's variant list: caught by
//     TestStdEnumShapeMatchesStdSource (it reads std's order directly) and by
//     the pinned text, which would print `int:7` for a value built as
//     `Json.Int(7)`. The tag numbering is a wrong ANSWER and not a Go type
//     error, so a fixture is what sees it if the shape check ever loosens.
//
//  2. `Arr` and `Obj` pointed at ONE rt field: a Go COMPILE error in this
//     package, because the two field types differ and the spec's kind is read
//     off rt by reflection.
//
//  3. The `Obj` payload made `Map<String, String>` in the spec: caught by
//     TestStdEnumShapeMatchesStdSource, because declaredShapeOK compares the
//     ELEMENT NAME against the row `ref` names. The reflection guards alone do
//     not catch it: `rt.Map[string, string]` is a legal Go type.
//
//  4. The shells returned WITHOUT `rtDeclared` from stdEnumDefs' first pass:
//     caught by TestStdEnumDefsArePackageNeutral, because `listKindIn(nil, …)`
//     PANICS with "no gen to intern the non-neutral structural type". So the
//     two-pass ordering is load-bearing in a way the spec table does not show.

// TestStdEnumJson_PinnedText spells the transcript out absolutely.
//
// Kept beside the golden comparison rather than replaced by it, because the two
// fail on different mutations: a golden comparison passes when the recording
// is wrong, and everything below routes through std/json.nomi's own `impl
// Display` and `Json.encode`. The absolute text is what catches a change in
// those.
func TestStdEnumJson_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fixture")
	}
	path := fixture("stdenum_json.nomi")
	want := strings.Join([]string{
		// Six payload positions, one per rt field. calendar.Error's five
		// variants share one `Msg`, so no existing fixture could see a spec
		// that pointed two variants at one field; here every field is a
		// different Go type.
		"string:hi",
		"int:7",
		"float:2.5",
		"bool:True",
		"arr:3",
		"obj:2",
		"null",
		// The empty container shapes: a nil *List, and the ZERO Map, which is
		// the empty map and needed no constructor.
		"arr:0",
		"obj:0",
		// The nested map pattern: descend an Obj, match a String key, match the
		// VARIANT of the value bound there. Three mechanisms, one arm.
		"named Ada",
		"object without a String name",
		"not an object",
		// encode: a hand-built object keeps its INSERTION order.
		`{"x":1,"y":2.5,"ok":true}`,
		// The documented lossiness — a whole Float loses its point, because
		// JSON has one number type and 'g' with -1 precision is the shortest
		// round-tripping form.
		"36",
		`[null,"q\"r"]`,
		// decode, then encode: object keys come back SORTED, which is the
		// OTHER ordering and it disagrees with the line above on purpose.
		"object",
		`{"a":1,"b":2,"c":[true,null]}`,
		// Int and Float are split by the LEXEME and not by the value.
		"int",
		"float",
		// The error vocabulary, reached through the TYPE-QUALIFIED spelling
		// `Json.DecodeError.to_string(e)`. These five strings are a designed
		// contract; see
		// tests/18-ffi-and-dynamic/json_decode_error_text_test.nomi.
		"json decode error at line 1, col 2: unexpected character 'o'",
		"json decode error at line 1, col 6: unexpected end of input: incomplete JSON value",
		"json decode error at line 1, col 1: unexpected end of input: expected a JSON value",
		"json decode error at line 1, col 4: trailing content after JSON value",
		"json decode error at line 1, col 1: number out of range: 1e999",
		// Display routes through std's `impl Display for Json`, which is
		// `Json.encode(jv)` — so these only read this way if the emitted value
		// reaches THAT impl rather than something of the same shape.
		"display:42",
		`display_obj:{"k":false}`,
		// THE NEGATIVE CONTROL: a user enum with container payloads, declared
		// in the fixture. Every line above routes through the process-wide std
		// def, so a change that made the std path the ONLY path would leave
		// this file green while breaking every user enum.
		"items:2",
		"named:1",
		"empty",
		"",
	}, "\n")
	if got := vmReference(path); got.stdout != want || got.exit != 0 {
		t.Fatalf("reference output is not what this fixture pins: %s\nwant:\n%s", got, want)
	}
}

// TestStdEnumJson_SelfReferentialPayloadsAreShared is the identity claim the
// whole mechanism rests on, asserted rather than argued.
//
// The two container payloads must be the SAME `*compKind` a call site's own
// `g.listKind` / `g.mapKind` produces, because a stdFunc's parameter and result
// kinds are compared BY POINTER against a call site's in a different gen. A
// per-gen kind here would match no call site anywhere — and it would not fail
// loudly, it would just refuse everything.
func TestStdEnumJson_SelfReferentialPayloadsAreShared(t *testing.T) {
	d := stdEnumDefs()[stdEnumJson]
	jsonKind := named(d)
	arr := d.variant("Arr")
	obj := d.variant("Obj")
	if arr == nil || obj == nil {
		t.Fatalf("Arr or Obj is missing from the def")
	}
	// The payload of `Arr` is `List<Json>` over THIS def, by pointer.
	wantArr := listKindIn(nil, jsonKind)
	if got := arr.payloads[0].k; got != wantArr {
		t.Errorf("Arr carries %s, want the shared %s", got.nomi(), wantArr.nomi())
	}
	wantObj := mapKindIn(nil, kindString, jsonKind)
	if got := obj.payloads[0].k; got != wantObj {
		t.Errorf("Obj carries %s, want the shared %s", got.nomi(), wantObj.nomi())
	}
	// SELF-REFERENCE, spelled out: the element of the list payload IS this def.
	if wantArr.comp == nil || len(wantArr.comp.parts) != 1 || wantArr.comp.parts[0].def != d {
		t.Errorf("List<Json>'s element is not this def; the self-reference did not close")
	}
	// And the payloads are PROCESS-WIDE, which is what a nil gen means: asking
	// again yields the same pointer. A per-gen intern would answer a different
	// one and nothing else here would notice.
	if listKindIn(nil, jsonKind).comp != wantArr.comp {
		t.Errorf("List<Json> is not interned process-wide")
	}
}

// TestStdEnumJson_RecursivePayloadKindsAreNeutral is the precondition the
// shared def rests on. A payload need not be a SCALAR: the requirement is
// `kind.packageNeutral`, and a scalar is only the strongest form of it. This
// asserts that predicate over the two container payloads.
func TestStdEnumJson_RecursivePayloadKindsAreNeutral(t *testing.T) {
	jsonKind := named(stdEnumDefs()[stdEnumJson])
	for _, k := range []kind{
		jsonKind,
		listKindIn(nil, jsonKind),
		mapKindIn(nil, kindString, jsonKind),
	} {
		if !k.packageNeutral() {
			t.Errorf("%s is not package-neutral, so the shared def is not sound", k.nomi())
		}
	}
	// The rendering is the thing neutrality is ABOUT, so it is checked too: an
	// `rt.`-prefixed spelling with no generated-package identifier in it.
	for _, want := range []struct {
		k    kind
		want string
	}{
		{jsonKind, "Json"},
		{listKindIn(nil, jsonKind), "List<Json>"},
		{mapKindIn(nil, kindString, jsonKind), "Map<String, Json>"},
	} {
		if got := want.k.nomi(); got != want.want {
			t.Errorf("%s renders %q, want %q", want.k.nomi(), got, want.want)
		}
	}
}

// TestStdEnumJson_ARefusedPayloadProducesNoAnchor is the shape check's negative
// half, and it is the one the reflection guards cannot supply.
//
// The builder lowers constructions and `case` arms from the SPEC, so an
// annotation that disagreed with the spec must produce NO anchor rather than
// lower against a layout nobody wrote. For a container payload the disagreement
// is not a Go type error — `rt.Map[string, string]` is perfectly legal — so this
// is asserted directly against declaredShapeOK, over the four shapes std could
// drift to.
func TestStdEnumJson_ARefusedPayloadProducesNoAnchor(t *testing.T) {
	simple := func(name string) ast.TypeExpr { return &ast.SimpleType{Name: name} }
	generic := func(name string, args ...ast.TypeExpr) ast.TypeExpr {
		return &ast.GenericType{Name: name, Params: args}
	}
	spec := &stdEnumSpecs[stdEnumJson]
	arr := spec.variants[4]
	obj := spec.variants[5]
	if arr.nomi != "Arr" || obj.nomi != "Obj" {
		t.Fatalf("the two container variants moved: got %q and %q", arr.nomi, obj.nomi)
	}
	// The shapes that MUST be accepted, as std writes them. Asserted first: a
	// negative-only table passes when the predicate answers false for
	// everything, which is the shape that makes a guard vacuous.
	if !arr.declaredShapeOK(generic("List", simple("Json")), stdEnumSpecs) {
		t.Errorf("List<Json> must be accepted for Arr")
	}
	if !obj.declaredShapeOK(generic("Map", simple("String"), simple("Json")), stdEnumSpecs) {
		t.Errorf("Map<String, Json> must be accepted for Obj")
	}
	for _, bad := range []struct {
		name string
		v    stdEnumVariantSpec
		te   ast.TypeExpr
	}{
		{"a widened element", arr, generic("List", simple("String"))},
		{"a different container", arr, generic("Map", simple("String"), simple("Json"))},
		{"a bare element", arr, simple("Json")},
		{"a widened Map value", obj, generic("Map", simple("String"), simple("Int"))},
		{"a retyped Map key", obj, generic("Map", simple("Int"), simple("Json"))},
		{"a one-argument Map", obj, generic("Map", simple("Json"))},
		{"the wrong container for Obj", obj, generic("List", simple("Json"))},
	} {
		if bad.v.declaredShapeOK(bad.te, stdEnumSpecs) {
			t.Errorf("%s (%s) was accepted for %s; it must produce no anchor",
				bad.name, bad.te.TypeString(), bad.v.nomi)
		}
	}
	// A SCALAR row must still refuse a container, which is the other direction
	// and the one a container-only table would not cover.
	if spec.variants[1].declaredShapeOK(generic("List", simple("Int")), stdEnumSpecs) {
		t.Errorf("Int accepted List<Int>")
	}
}
