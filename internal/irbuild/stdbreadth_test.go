package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestStdEnum_StructPayloadShapeIsChecked holds the IOError anchor's SHAPE check
// against the mutations that would make it lower against a declaration nobody
// wrote.
//
// The shape check is what turns a spec from a claim into an anchor, and its
// failure mode is silent: a spec that accepted a wider declaration would write a
// value into a field that does not hold one. Four mutations, each a plausible
// std edit, and each must produce NO anchor:
//
//	the field RENAMED             `NotFound {file: String}`
//	the field RETYPED             `NotFound {path: Int}`
//	a SECOND field added          `NotFound {path: String, code: Int}`
//	a DEFAULT added               `NotFound {path: String = ""}`
//
// Asserted at the spec level rather than through a program, because the thing
// being checked is `declaredStructShapeOK` and a program cannot vary std's
// declaration.
func TestStdEnum_StructPayloadShapeIsChecked(t *testing.T) {
	spec := stdEnumSpecForTest(t, "IOError")
	v := &spec.variants[0]
	if v.form != payloadStructScalar || v.nomiField != "path" {
		t.Fatalf("IOError's first variant is not the struct-shaped `path` row this test varies: %+v", v)
	}

	// The POSITIVE CONTROL first: the declaration std actually carries must be
	// accepted, or every rejection below is vacuous.
	if !v.declaredStructShapeOK(structFieldsForTest("path", "String", false)) {
		t.Fatal("the shape check rejects std's own declaration, so the rejections below measure nothing")
	}
	for _, bad := range []struct {
		name   string
		fields []astStructFieldSpec
	}{
		{"renamed", []astStructFieldSpec{{"file", "String", false}}},
		{"retyped", []astStructFieldSpec{{"path", "Int", false}}},
		{"second field", []astStructFieldSpec{{"path", "String", false}, {"code", "Int", false}}},
		{"defaulted", []astStructFieldSpec{{"path", "String", true}}},
		{"empty", nil},
	} {
		if v.declaredStructShapeOK(structFieldsFromSpecs(bad.fields)) {
			t.Errorf("%s: the shape check ACCEPTED a declaration std does not carry", bad.name)
		}
	}
}

// TestIOErrorSlotsMatchRTFieldNames holds the spec's Go field names against
// rt.IOError by reflection.
//
// TestStdEnumSlotsMatchRT already does this for every row; this one adds the half
// that is specific to a struct-shaped payload and that reflection over slots
// cannot see: the NOMI field name, which is what a pattern binds. A spec whose
// `nomiField` disagreed with std's declaration produces no anchor (the shape
// check above), but a spec whose `nomiField` disagreed with the VARIANTDEF's
// payload would bind nothing and emit a selector for a field the pattern never
// mentioned — so both directions are pinned here.
func TestIOErrorSlotsMatchRTFieldNames(t *testing.T) {
	i := stdEnumSpecIndexForTest(t, "IOError")
	d := stdEnumDefs()[i]
	for _, v := range d.variants {
		if len(v.payloads) != 1 {
			t.Fatalf("%s carries %d payloads, want 1", v.nomi, len(v.payloads))
		}
		if v.kind != "struct" {
			t.Errorf("%s is a %q variant, want struct", v.nomi, v.kind)
		}
		if v.payloads[0].nomi == "" {
			t.Errorf("%s's payload has no Nomi field name, so a struct pattern binds nothing", v.nomi)
		}
	}
	// And the Nomi names are the ones std declares, absolutely: these are what
	// `IOError.NotFound{path}` binds, and a permuted pair is a wrong answer
	// rather than a compile error.
	want := map[string]string{"NotFound": "path", "Other": "reason"}
	if len(want) != len(d.variants) {
		t.Fatalf("%d variants, want %d", len(d.variants), len(want))
	}
	for _, v := range d.variants {
		if got := want[v.nomi]; got != v.payloads[0].nomi {
			t.Errorf("%s binds %q, std declares %q", v.nomi, v.payloads[0].nomi, got)
		}
	}
}

// TestIOHostPackageIsTheFourthAndCarriesNoCompilerCode asserts what makes the
// filesystem binding legitimate: the implementation is reachable by a program
// and it is NOT in rt, which carries no filesystem effect.
//
// Both halves, because either alone is satisfiable the wrong way. A row in
// hostPackages with the implementation in rt would satisfy "reachable"; an
// implementation outside rt that hostPackages did not know would be refused by
// hostCallName and the binding would silently not exist.
func TestIOHostPackageIsTheFourthAndCarriesNoCompilerCode(t *testing.T) {
	found := false
	for _, hp := range hostPackages {
		if hp.path == ioHostPath {
			found = true
			if hp.local != "stdio" {
				t.Errorf("local name is %q; header() emits the path unaliased, so it must be the last element", hp.local)
			}
		}
	}
	if !found {
		t.Fatalf("%s is not in hostPackages, so hostCallName cannot resolve its symbols", ioHostPath)
	}
	for _, name := range []string{"io.read_file", "io.write_file"} {
		h, ok := stdlibHostFuncs[name]
		if !ok {
			t.Errorf("%s is not bound", name)
			continue
		}
		if h.pkg != ioHostPath {
			t.Errorf("%s is bound in %s, want %s — rt must not carry filesystem effect", name, h.pkg, ioHostPath)
		}
		if strings.HasPrefix(h.call, "rt.") {
			t.Errorf("%s is emitted as %s, i.e. from rt", name, h.call)
		}
	}
}

// --- helpers ---------------------------------------------------------------

// astStructFieldSpec is one field of a hypothetical std declaration, for the
// shape-check cases above.
type astStructFieldSpec struct {
	name       string
	typeName   string
	hasDefault bool
}

func stdEnumSpecForTest(t *testing.T, nomi string) *stdEnumSpec {
	t.Helper()
	return &stdEnumSpecs[stdEnumSpecIndexForTest(t, nomi)]
}

func stdEnumSpecIndexForTest(t *testing.T, nomi string) int {
	t.Helper()
	for i := range stdEnumSpecs {
		if stdEnumSpecs[i].nomi == nomi {
			return i
		}
	}
	t.Fatalf("no stdEnumSpecs row named %q", nomi)
	return -1
}

// structFieldsFromSpecs builds the ast.StructField list a hypothetical std
// declaration would carry. The default is an arbitrary non-nil node, because
// declaredStructShapeOK asks only whether one is PRESENT.
func structFieldsFromSpecs(specs []astStructFieldSpec) []ast.StructField {
	out := make([]ast.StructField, 0, len(specs))
	for _, s := range specs {
		f := ast.StructField{Name: s.name, TypeAnnotation: &ast.SimpleType{Name: s.typeName}}
		if s.hasDefault {
			f.Default = &ast.StringLit{Value: ""}
		}
		out = append(out, f)
	}
	return out
}

func structFieldsForTest(name, typeName string, hasDefault bool) []ast.StructField {
	return structFieldsFromSpecs([]astStructFieldSpec{{name, typeName, hasDefault}})
}
