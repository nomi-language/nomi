package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractImports(t *testing.T) {
	src := `import math: math
import models.{User, Point}

fn main() {
    math.double(21)
}
`
	dir := "/project"
	imports := ExtractImports(src, dir)

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %v", len(imports), imports)
	}

	expected := map[string]bool{
		"/project/math.nomi":   true,
		"/project/models.nomi": true,
	}
	for _, imp := range imports {
		if !expected[imp] {
			t.Errorf("unexpected import: %s", imp)
		}
	}
}

// Block-form imports (`import { a, b.{X}, c }`) parse to *ast.ImportBlock,
// not a flat list of *ast.ImportStmt. ExtractImportsFromNodes used to skip
// every node that wasn't directly an *ast.ImportStmt — silently dropping
// every import inside a block. This left the LSP's reverse-dependency map
// empty for files using block-form imports, so renaming an exported symbol
// in a parent module never propagated fresh diagnostics to dependents
// until LSP restart (the scenario reported on tests/15-app-and-defer/effects/).
func TestExtractImports_BlockForm(t *testing.T) {
	src := `import {
  std/io
  math: math
  models.{User, Point}
}

fn main() {}
`
	dir := "/project"
	imports := ExtractImports(src, dir)

	if len(imports) != 2 {
		t.Fatalf("expected 2 user imports (std/io filtered out), got %d: %v", len(imports), imports)
	}
	expected := map[string]bool{
		"/project/math.nomi":   true,
		"/project/models.nomi": true,
	}
	for _, imp := range imports {
		if !expected[imp] {
			t.Errorf("unexpected import: %s", imp)
		}
	}
}

// Mixed standalone and block imports must both be picked up.
func TestExtractImports_MixedStandaloneAndBlock(t *testing.T) {
	src := `import math: math

import {
  models.{User}
  utils: utils
}

import shared: Shared
`
	imports := ExtractImports(src, "/project")
	if len(imports) != 4 {
		t.Fatalf("expected 4 imports, got %d: %v", len(imports), imports)
	}
	expected := map[string]bool{
		"/project/math.nomi":   true,
		"/project/models.nomi": true,
		"/project/utils.nomi":  true,
		"/project/shared.nomi": true,
	}
	for _, imp := range imports {
		if !expected[imp] {
			t.Errorf("unexpected import: %s", imp)
		}
	}
}

func TestReverseDeps(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("fn double(x: Int): Int { x * 2 }"), 0644)
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("import math: math\nfn main() { math.double(21) }"), 0644)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")
	dm.IndexFile(mathURI)
	dm.IndexFile(mainURI)

	// main.nomi imports math.nomi, so math.nomi's reverse deps should include main.nomi
	deps := dm.ReverseDeps(mathURI)
	if len(deps) != 1 || deps[0] != mainURI {
		t.Errorf("expected [%s], got %v", mainURI, deps)
	}

	// main.nomi has no reverse deps
	deps = dm.ReverseDeps(mainURI)
	if len(deps) != 0 {
		t.Errorf("expected no reverse deps for main, got %v", deps)
	}
}

// Regression: reverse deps must be populated when the importer uses the
// block-form `import { ... }` syntax. Before the fix to
// ExtractImportsFromNodes, dm.reverseDeps stayed empty for any file whose
// imports lived inside a block, which caused PropagateChange to find no
// dependents and the LSP to skip publishing fresh diagnostics on rename.
func TestReverseDeps_BlockFormImport(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("pub fn double(x: Int): Int { x * 2 }"), 0644)
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("import {\n  math.{double}\n}\n\nfn main() { double(21) }\n"), 0644)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")
	dm.IndexFile(mathURI)
	dm.IndexFile(mainURI)

	deps := dm.ReverseDeps(mathURI)
	if len(deps) != 1 || deps[0] != mainURI {
		t.Errorf("expected reverse deps to include main.nomi (importer uses block form); got %v", deps)
	}
}
