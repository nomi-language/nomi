package frontend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// A derive in an imported file whose field type has no impl is reported at
// the field, in the file that holds the derive, with that file's source
// line. The error is the program's, found with the entry's, so it was printed
// with the entry's path at the imported file's line.
func TestDeriveMissingImplInAnImportedFileNamesThatFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import {
    std/io
    child.Holder
}

fn main() {
    io.inspect(Holder{n: 1, o: Opaque{f: |x| x}})
}
`,
		"child.nomi": `pub struct Opaque {
    f: (Int) -> Int
}

pub struct Holder {
    n: Int
    o: Opaque
}

derive Equatable for Holder
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	_, err := frontend.New(frontend.Config{}).CheckFileSource(entry, files["main.nomi"], frontend.Mode{})
	ds, ok := err.(frontend.Diagnostics)
	if !ok {
		t.Fatalf("the front end accepts a derive over a field with no Equatable impl: %v", err)
	}
	for _, d := range ds {
		if !strings.Contains(d.Message, "no impl of `Equatable` for `Opaque`") {
			continue
		}
		if filepath.Base(d.Path) != "child.nomi" || d.Line != 7 || d.Col != 5 || strings.TrimSpace(d.SourceLine) != "o: Opaque" {
			t.Fatalf("the missing impl is reported at %s:%d:%d (%q), want child.nomi:7:5 (\"o: Opaque\")", d.Path, d.Line, d.Col, d.SourceLine)
		}
		return
	}
	t.Fatalf("no missing-impl diagnostic for Opaque among %v", ds)
}
