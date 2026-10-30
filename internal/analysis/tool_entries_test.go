package analysis_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/std"
)

// A file under tools/ is a tool, and so an entry point, unless it is a test
// file or sits in an internal/ directory, where a tool's helpers go.
func TestToolEntry_IsToolFile(t *testing.T) {
	for key, want := range map[string]bool{
		"tools/choices":             true,
		"tools/db/seed":             true,
		"tools/internal":            true,
		"tools/choices_test":        false,
		"tools/internal/samples":    false,
		"tools/db/internal/rows":    false,
		"tools":                     false,
		"main":                      false,
		"cmd/tools/x":               false,
		"toolsx/choices":            false,
		"internal/tools/not_a_tool": false,
	} {
		if got := analysis.IsToolFile(key); got != want {
			t.Errorf("IsToolFile(%q) = %v, want %v", key, got, want)
		}
	}
}

// Entries lists entry_points first, in the manifest's order, then each tool
// it does not list, by path. A listed tool is not repeated.
func TestToolEntry_EntriesAddsTheTools(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"main.nomi", "tools/choices.nomi", "tools/db/seed.nomi", "tools/a.nomi",
		"tools/choices_test.nomi", "tools/internal/samples.nomi", "tools/notes.txt",
	} {
		writeToolEntryFile(t, root, rel, "")
	}
	m := &analysis.Manifest{Name: "cafe", EntryPoints: []string{"main", "tools/choices"}}
	if got, want := m.Entries(root), []string{"main", "tools/choices", "tools/a", "tools/db/seed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Entries = %q, want %q", got, want)
	}
	m = &analysis.Manifest{Name: "cafe", EntryPoints: []string{"main"}}
	if got, want := m.Entries(root), []string{"main", "tools/a", "tools/choices", "tools/db/seed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Entries = %q, want %q", got, want)
	}
	if !m.IsEntry("tools/choices") || !m.IsEntry("main") || m.IsEntry("tools/internal/samples") || m.IsEntry("enums") {
		t.Errorf("IsEntry answers wrong for main, tools/choices, tools/internal/samples or enums")
	}
}

// A tool's fn main needs no entry_points line, listed or not; a tool with
// no fn main is an error that says it is a tool; a helper under
// tools/internal/ needs none.
func TestToolEntry_PlacementTreatsToolsAsEntries(t *testing.T) {
	for _, entries := range [][]string{{"main"}, {"main", "tools/choices"}} {
		m := &analysis.Manifest{Name: "cafe", EntryPoints: entries}
		errs := analysis.CheckEntryPlacement(m, []ast.Node{toolMain()}, "main", map[string][]ast.Node{
			"tools/choices":          {toolMain()},
			"tools/internal/samples": {&ast.FuncDef{Name: "items", Line: 2, Col: 1}},
		})
		if len(errs) != 0 {
			t.Errorf("entry_points %q: %+v", entries, errs)
		}
	}
	m := &analysis.Manifest{Name: "cafe", EntryPoints: []string{"main"}}
	errs := analysis.CheckEntryPlacement(m, []ast.Node{toolMain()}, "main", map[string][]ast.Node{
		"tools/helper": {&ast.FuncDef{Name: "items", Line: 2, Col: 1}},
	})
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "tools/helper is a tool, since it is under tools/, but has no entry callback") {
		t.Errorf("a tool with no fn main: %+v", errs)
	}
	errs = analysis.CheckEntryPlacement(m, []ast.Node{toolMain()}, "main", map[string][]ast.Node{
		"helper": {toolMain()},
	})
	if len(errs) != 1 || strings.Contains(errs[0].Message, "—") ||
		!strings.Contains(errs[0].Message, "entry callback in helper but file is not declared in nomi.toml entry_points; add \"helper\" to entry_points") {
		t.Errorf("an unlisted fn main outside tools/: %+v", errs)
	}
}

// The language server takes a tool as an entry though nomi.toml does not
// list it: the tool's fn main is no error, and its helper under
// tools/internal/, analyzed through the tool, has none either.
func TestToolEntry_DocumentsSeeAnUnlistedTool(t *testing.T) {
	root := t.TempDir()
	writeToolEntryFile(t, root, "go.mod", "module cafe\n\ngo 1.26.3\n")
	writeToolEntryFile(t, root, "nomi.toml", "[module]\nname = \"cafe\"\nentry_points = [\"main\"]\n")
	writeToolEntryFile(t, root, "main.nomi", "import std/io\n\nfn main() {\n    io.print(\"cafe\")\n}\n")
	tool := writeToolEntryFile(t, root, "tools/greet.nomi", `import {
    std/io
    tools/internal/words
}

fn main() {
    io.print(words.greeting())
}
`)
	helper := writeToolEntryFile(t, root, "tools/internal/words.nomi", `pub fn greeting() -> String {
    "hello"
}
`)
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	for _, path := range []string{helper, tool} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := dm.Open(fileuri.FromPath(path), string(src))
		if doc == nil || doc.Analysis == nil {
			t.Fatalf("%s: no analysis", path)
		}
		if len(doc.Errors) != 0 || len(doc.Analysis.TypeErrors) != 0 {
			t.Errorf("%s: %+v %+v", filepath.Base(path), doc.Errors, doc.Analysis.TypeErrors)
		}
	}
}

func writeToolEntryFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func toolMain() *ast.FuncDef {
	return &ast.FuncDef{Name: "main", Line: 1, Col: 1}
}
