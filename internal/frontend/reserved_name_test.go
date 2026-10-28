package frontend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// A type declared under a reserved name is refused, and every use of the
// name still means the built-in: an impl for it is an orphan impl of the
// prelude Unit, a variant pattern against it fits no enum, and an import of
// it names nothing public. The refusal is reported alone, whether the
// refused file is the entry or a file the entry imports.
func TestReservedTypeNameIsReportedAlone(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"units.nomi": `pub enum Unit {
    Ohms
    Kiloohms
}

impl Display for Unit {
    fn to_string(unit: Unit) -> String {
        case unit {
            .Ohms -> "ohms"
            .Kiloohms -> "kiloohms"
        }
    }
}
`,
		"units_test.nomi": `import units.Unit

test "unit" {
    assert Display.to_string(Unit.Ohms) == "ohms"
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const want = "type name 'Unit' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyUnit { ... }`)"
	for _, entry := range []string{"units.nomi", "units_test.nomi"} {
		t.Run(entry, func(t *testing.T) {
			path := filepath.Join(dir, entry)
			_, err := frontend.New(frontend.Config{}).CheckFileSource(path, files[entry], frontend.Mode{})
			ds, ok := err.(frontend.Diagnostics)
			if !ok {
				t.Fatalf("the front end accepts an enum named Unit: %v", err)
			}
			if len(ds) != 1 {
				t.Fatalf("got %d diagnostics, want the refusal alone:\n%v", len(ds), ds)
			}
			if d := ds[0]; d.Message != want || filepath.Base(d.Path) != "units.nomi" || d.Line != 1 || d.Col != 10 {
				t.Fatalf("got %s:%d:%d %q, want units.nomi:1:10 %q", d.Path, d.Line, d.Col, d.Message, want)
			}
		})
	}
}
