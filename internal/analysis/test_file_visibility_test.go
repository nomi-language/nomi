package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A file named after another with `_test` before `.nomi` is an ordinary
// file of its module (spec §14 *Visibility*): shapes_test.nomi sees
// shapes.nomi as every other file of the module does. It does not name
// shapes.nomi's private declarations, build or take apart its opaque
// types, or derive for its types; each is the error any other file gets.

// shapesFile declares a private function and struct, an internal opaque
// type with a function that builds one, and an internal struct.
const shapesFile = `private struct Hidden
    size: Int
end

private fn area(h: Hidden) -> Int
    h.size * h.size
end

opaque type Area Int

fn area_of(n: Int) -> Area
    Area(n + area(Hidden{size: 1}))
end

struct Shape
    size: Int
end
`

// testFileErrors checks shapes.nomi beside shapes_test.nomi, which holds
// test, and answers shapes_test's diagnostics. It fails when shapes.nomi
// has any, so each case's errors are the test file's.
func testFileErrors(t *testing.T, test string) []analysis.TypeError {
	t.Helper()
	p := newManager().CheckProgram("/nomi-test-file-visibility", "shapes_test", map[string]string{
		"shapes":      shapesFile,
		"shapes_test": test,
	})
	if errs := p.Errors["shapes"]; len(errs) > 0 {
		t.Fatalf("shapes.nomi does not check:%s", diagList(errs))
	}
	return p.Errors["shapes_test"]
}

// withCode answers the errors of errs with code.
func withCode(errs []analysis.TypeError, code string) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == code {
			out = append(out, e)
		}
	}
	return out
}

func TestTestFile_DoesNotNameItsNeighboursPrivateDeclarations(t *testing.T) {
	errs := testFileErrors(t, `import shapes.{Hidden, area}

test "area"
    assert area(Hidden{size: 3}) == 9
end
`)
	found := withCode(errs, analysis.PrivateDeclarationCode)
	if len(found) == 0 {
		t.Fatalf("shapes_test.nomi names shapes.nomi's private declarations:%s", diagList(errs))
	}
	names := map[string]bool{}
	for _, e := range found {
		if len(e.Args) != 2 || e.Args[1] != "shapes.nomi" {
			t.Errorf("%d:%d %s has args %v, want a name and shapes.nomi", e.Line, e.Col, e.Message, e.Args)
			continue
		}
		names[e.Args[0]] = true
		if want := "`" + e.Args[0] + "` is private to `shapes.nomi`"; e.Message != want {
			t.Errorf("the error says %q, want %q", e.Message, want)
		}
	}
	if !names["area"] || !names["Hidden"] {
		t.Errorf("the errors name %v, want area and Hidden", names)
	}
}

func TestTestFile_DoesNotBuildItsNeighboursOpaqueType(t *testing.T) {
	errs := testFileErrors(t, `import shapes.{self, Area}

test "area"
    assert shapes.area_of(3) == Area(4)
end
`)
	found := withCode(errs, analysis.OpaqueConstructionCode)
	if len(found) != 1 {
		t.Fatalf("want one opaque construction error, got:%s", diagList(errs))
	}
	if e := found[0]; len(e.Args) != 1 || e.Args[0] != "Area" || e.Line != 4 {
		t.Fatalf("the error is at line %d with args %v, want line 4 and Area", e.Line, e.Args)
	}
}

func TestTestFile_DoesNotDeriveForItsNeighboursType(t *testing.T) {
	errs := testFileErrors(t, `import shapes.Shape

derive Equatable for Shape

test "shape"
    assert Shape{size: 1} == Shape{size: 1}
end
`)
	found := withCode(errs, analysis.DeriveOutsideTypesFileCode)
	if len(found) != 1 {
		t.Fatalf("want one derive error, got:%s", diagList(errs))
	}
	e := found[0]
	if len(e.Args) != 1 || e.Args[0] != "Shape" || e.Line != 3 {
		t.Fatalf("the error is at line %d with args %v, want line 3 and Shape", e.Line, e.Args)
	}
	if want := "`derive ... for Shape` must be written in the file that declares `Shape`"; e.Message != want {
		t.Fatalf("the error says %q, want %q", e.Message, want)
	}
}
