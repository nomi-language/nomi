package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestOutcome_FailurePayloadMatchesStdSource validates the payload enum's own
// declaration, which `preludeSpec.matches` cannot see.
//
// `matches` receives `Outcome`'s `*ast.EnumDef` and checks that its `Failed`
func TestOutcome_FailurePayloadMatchesStdSource(t *testing.T) {
	lib := std.Load()
	if lib == nil {
		t.Fatal("std.Load() returned nothing, so this guard asserts nothing")
	}
	if len(namedPayloadSpecs) == 0 {
		t.Fatal("namedPayloadSpecs is empty, so this guard asserts nothing")
	}
	for _, spec := range namedPayloadSpecs {
		module, isStd := strings.CutPrefix(spec.origin, "std/")
		if !isStd {
			t.Fatalf("%s.%s: origin is not a std module, so no source can validate it",
				spec.origin, spec.nomi)
		}
		var decl *ast.EnumDef
		for _, n := range lib.Nodes[module] {
			ed, isEnum := n.(*ast.EnumDef)
			if isEnum && ed.Name == spec.nomi {
				decl = ed
				break
			}
		}
		if decl == nil {
			t.Fatalf("%s no longer declares %s; every mention of the type that references "+
				"it as a payload is lowered against a shape std does not have",
				spec.origin, spec.nomi)
		}
		if !spec.matchesDecl(decl) {
			t.Fatalf("%s.%s's declaration no longer matches the spec: the variant list "+
				"decides the TAGS, and a permuted tag is a wrong answer rather than a "+
				"compile error", spec.origin, spec.nomi)
		}
	}
	// The CO-DECLARATION, which is the whole licence for the name check inside
	// preludeSpec.matches. Asserted rather than assumed, and asserted from the
	// referencing side so a spec whose payload drifted to another module fails
	// here by name.
	pairs := 0
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		for _, v := range spec.variants {
			if v.namedPayload == nil {
				continue
			}
			pairs++
			if v.namedPayload.origin != spec.origin {
				t.Fatalf("%s.%s's payload %s is declared in %q while %s is declared in %q.\n"+
					"preludeSpec.matches compares that payload by NAME, and only the "+
					"co-declaration makes a name check an identity check: a file-local "+
					"`pub enum` cannot be shadowed by an import, but a payload in ANOTHER "+
					"module can be.",
					spec.nomi, v.nomi, v.namedPayload.nomi, v.namedPayload.origin,
					spec.nomi, spec.origin)
			}
		}
	}
	if pairs == 0 {
		t.Fatal("no preludeSpec variant references a named payload, so the co-declaration " +
			"assertion above is vacuous")
	}
}

// TestOutcome_LayoutMatchesRT holds the builder's belief about rt.Failure
// against rt.Failure.
//
// TestPreludeLayoutMatchesRT covers `rt.Outcome[T]` because Outcome is a
// preludeSpec. `Failure` is not a preludeSpec — it is a named PAYLOAD, and
// nothing else reflects over it, so its slot layout would otherwise be
// unchecked. The builder's kind text spells `Msg` as a field selector, and
// this test holds rt to declaring it.
func TestOutcome_LayoutMatchesRT(t *testing.T) {
	for _, spec := range namedPayloadSpecs {
		got := spec.goType
		if _, ok := got.FieldByName(spec.tagField); !ok {
			t.Fatalf("%s has no field %q, which is what the builder writes the tag into",
				got, spec.tagField)
		}
		d := spec.def()
		// One slot per DISTINCT field name, which is the spec's own dedup rule
		// and the reason rt declares a single `Msg` for two variants.
		fields := 1
		seen := map[string]bool{}
		for _, v := range spec.variants {
			// kindInvalid: marker — the spec's "this variant carries nothing".
			if v.payload == kindInvalid {
				continue
			}
			if !seen[v.field] {
				seen[v.field] = true
				fields++
			}
			f, ok := got.FieldByName(v.field)
			if !ok {
				t.Fatalf("%s has no field %q for variant %s", got, v.field, v.nomi)
			}
			if !f.IsExported() {
				t.Fatalf("%s.%s is unexported; the kind text names it from another package", got, v.field)
			}
			if k := kindOfGoType(f.Type); k != v.payload {
				t.Fatalf("%s.%s is %s, but the spec says %s carries %s",
					got, v.field, f.Type, v.nomi, v.payload.nomi())
			}
		}
		if got.NumField() != fields {
			t.Fatalf("%s has %d fields, the builder accounts for %d — an unaccounted field "+
				"is storage the builder never writes", got, got.NumField(), fields)
		}
		if len(d.slots) != len(seen) {
			t.Fatalf("%s's def has %d slots for %d distinct field names; the dedup is the "+
				"spec's to state and the def builder's to honour", spec.nomi, len(d.slots), len(seen))
		}
	}
	// rt's own constructors, spelled out rather than derived: these are the
	// values a generated `case` arm compares against.
	if rt.Panicked("x").Tag != 1 || rt.Errored("x").Tag != 2 {
		t.Fatal("rt disagrees with Panicked=1 Errored=2")
	}
	if rt.Completed[int64](0).Tag != 1 || rt.Cancelled[int64]().Tag != 2 ||
		rt.Failed[int64](rt.Failure{}).Tag != 3 {
		t.Fatal("rt disagrees with Completed=1 Cancelled=2 Failed=3")
	}
}
