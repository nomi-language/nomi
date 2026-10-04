package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIRIsFreeOfTheFrontEnd checks that `internal/ir` imports neither the
// front end nor this package.
//
// The IR sits between the checked AST and the VM, and it is serialized into
// `nomi build`'s images. A representation carrying front-end types could not
// be serialized without dragging the analyzer along, so the resolvers that
// answer with the front end's name resolution stay in `internal/irbuild` and
// hand the IR their answers.
//
// The positive is planted on a synthetic source, so the check says the
// detector works without needing a violation to exist.
func TestIRIsFreeOfTheFrontEnd(t *testing.T) {
	forbidden := []string{"github.com/nomi-language/nomi/internal/analysis", "github.com/nomi-language/nomi/internal/ast", "github.com/nomi-language/nomi/internal/parser", "github.com/nomi-language/nomi/internal/irbuild"}
	dir := filepath.Join("..", "ir")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	files := 0
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		files++
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", n, err)
		}
		for _, bad := range irImportsAmong(f, forbidden) {
			t.Errorf("internal/ir/%s imports %s. The IR would become a client of the "+
				"front end; the front-end resolvers stay in internal/irbuild so that it "+
				"does not", n, bad)
		}
	}
	if files == 0 {
		t.Fatal("no non-test sources in internal/ir, so this test passes vacuously")
	}

	const planted = "package ir\n\nimport \"github.com/nomi-language/nomi/internal/analysis\"\n\nvar _ = analysis.Pos{}\n"
	pf, err := parser.ParseFile(token.NewFileSet(), "planted.go", planted, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse the planted source: %v", err)
	}
	if got := irImportsAmong(pf, forbidden); len(got) == 0 {
		t.Fatal("the detector found nothing in a source that imports nomi/analysis, so the " +
			"assertion above holds vacuously")
	}
	t.Logf("internal/ir: %d non-test sources, none importing %v", files, forbidden)
}

// irImportsAmong is every forbidden path f imports.
func irImportsAmong(f *ast.File, forbidden []string) []string {
	var out []string
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, bad := range forbidden {
			if path == bad {
				out = append(out, path)
			}
		}
	}
	return out
}
