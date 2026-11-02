package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shapesSource declares a private function, type and enum, and an opaque
// type, for the partner-test visibility tests.
const shapesSource = `enum Kind {
    Round
    Square
}

struct Shape {
    kind: Kind
    size: Int
}

pub opaque type Area Int

fn area(shape: Shape) -> Area {
    case shape.kind {
        .Round -> Area(shape.size * 3)
        .Square -> Area(shape.size)
    }
}
`

// partnerProject writes shapes.nomi and the files given into a fresh
// directory and checks the file named entry, as nomi check does.
func partnerProject(t *testing.T, entry string, files map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	files["shapes.nomi"] = shapesSource
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Check(filepath.Join(dir, entry))
}

const readsPrivate = `import shapes.{Area, Kind, Shape, area}

test "reads" {
    Area(n) = area(Shape{kind: Kind.Square, size: 2})
    assert n == 2
}
`

// shapes_test.nomi, the partner test of shapes.nomi, imports and uses its
// private function, struct and enum and opens its opaque type.
func TestPartnerTestFile_ReadsItsPartnersPrivateDeclarations(t *testing.T) {
	if err := partnerProject(t, "shapes_test.nomi", map[string]string{"shapes_test.nomi": readsPrivate}); err != nil {
		t.Fatalf("the partner test file is refused its partner's private declarations: %v", err)
	}
}

// Any other file importing the same private names gets the error it always
// got, whatever its name: a third file, and a `_test` file named after no
// sibling, which is nobody's partner.
func TestPartnerTestFile_NoOtherFileReadsPrivateDeclarations(t *testing.T) {
	for _, entry := range []string{"other.nomi", "other_test.nomi", "shape_test.nomi"} {
		t.Run(entry, func(t *testing.T) {
			err := partnerProject(t, entry, map[string]string{entry: readsPrivate})
			if err == nil {
				t.Fatalf("%s reads shapes.nomi's private declarations, though it is not its partner test", entry)
			}
			for _, want := range []string{"'area' is private and cannot be imported", "'Shape' is private and cannot be imported"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not say %q:\n%v", want, err)
				}
			}
		})
	}
}

// The partner test reads private names without re-exporting them: a file
// importing from shapes_test.nomi cannot reach shapes.nomi's private area.
func TestPartnerTestFile_DoesNotReexport(t *testing.T) {
	files := map[string]string{
		"shapes_test.nomi": readsPrivate,
		"user.nomi":        "import shapes_test.{area}\n\nfn main() {}\n",
	}
	if err := partnerProject(t, "user.nomi", files); err == nil {
		t.Fatal("a file reaches shapes.nomi's private area through its partner test")
	}
}
