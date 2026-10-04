package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func TestDocumentManager_OpenAndGet(t *testing.T) {
	dm := NewDocumentManager()
	dm.Open("file:///test.nomi", "fn add(x: Int, y: Int): Int { x + y }")

	doc := dm.Get("file:///test.nomi")
	if doc == nil {
		t.Fatal("expected document to be tracked")
	}
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	if doc.Analysis.ModuleScope.Lookup("add") == nil {
		t.Fatal("expected add in module scope")
	}
}

// A stdlib file opened directly in the editor (e.g. via go-to-def into
// ~/.cache/nomi/stdlib/maybe.nomi or by browsing stdlib/
// in-tree) must not be analyzed with the prelude as its parent scope —
// stdlib files import their dependencies by hand, and giving them the
// prelude would trip the reserved-name check on every prelude-exported
// type the stdlib file itself declares (Maybe, Result, Display, ...).
// Detected by an immediate-parent directory named "stdlib".
func TestDocumentManager_StdlibFileBypassesPrelude(t *testing.T) {
	dm := NewDocumentManager()
	// Wire a non-nil prelude scope so the reserved-name check would fire
	// against `Maybe` if the bypass weren't honoured.
	primitives := NewScope(nil)
	primitives.Define(&Symbol{Name: "Maybe", Kind: SymbolEnum, Pos: Pos{Line: 0, Col: 0}})
	dm.SetStdlib(primitives, nil, nil)

	dm.Open("file:///path/to/std/maybe.nomi", "pub enum Maybe<T> { Some T; None }")

	doc := dm.Get("file:///path/to/std/maybe.nomi")
	if doc == nil {
		t.Fatal("expected document tracked")
	}
	for _, err := range doc.Analysis.TypeErrors {
		if strings.HasPrefix(err.Message, "type name 'Maybe' is reserved") {
			t.Errorf("stdlib file should bypass prelude; got reserved-name error: %s", err.Message)
		}
	}
}

func TestDocumentManager_Update(t *testing.T) {
	dm := NewDocumentManager()
	dm.Open("file:///test.nomi", "fn add(x: Int, y: Int): Int { x + y }")
	dm.Update("file:///test.nomi", "fn sub(x: Int, y: Int): Int { x - y }")

	doc := dm.Get("file:///test.nomi")
	if doc.Analysis.ModuleScope.Lookup("sub") == nil {
		t.Fatal("expected sub after update")
	}
	if doc.Analysis.ModuleScope.Lookup("add") != nil {
		t.Error("add should no longer exist after update")
	}
}

func TestDocumentManager_CloseIndexesTheDiskText(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.nomi")
	os.WriteFile(filePath, []byte("fn disk_version(): Int { 1 }"), 0644)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	uri := "file://" + filePath

	doc := dm.Open(uri, "fn editor_version(): Int { 2 }")
	if doc.Analysis.ModuleScope.Lookup("editor_version") == nil {
		t.Fatal("expected editor_version while open")
	}

	// Closing drops the analysis and indexes the disk text.
	if !dm.Close(uri) {
		t.Fatal("Close of a file on disk should report it is still a workspace file")
	}
	if dm.Get(uri) != nil || dm.IsOpen(uri) {
		t.Fatal("expected no open document after Close")
	}
	decls := dm.FileDecls(filePath)
	if len(decls) != 1 || decls[0].Name != "disk_version" {
		t.Errorf("expected the index to hold disk_version after Close, got %+v", decls)
	}
	snap := dm.Analyzed(uri)
	if snap == nil || snap.Open || snap.Analysis.ModuleScope.Lookup("disk_version") == nil {
		t.Error("expected an on-demand analysis of the disk text after Close")
	}
}

func TestDocumentManager_CloseRemovesNonexistentFile(t *testing.T) {
	dm := NewDocumentManager()
	dm.Open("file:///nonexistent/test.nomi", "fn add(x: Int, y: Int): Int { x + y }")
	dm.Close("file:///nonexistent/test.nomi")
	// File doesn't exist on disk, so it should be removed entirely
	if dm.Get("file:///nonexistent/test.nomi") != nil {
		t.Error("expected document to be removed when file doesn't exist on disk")
	}
}

func TestDocumentManager_StoresNodes(t *testing.T) {
	dm := NewDocumentManager()
	doc := dm.Open("file:///test.nomi", "fn add(x: Int, y: Int): Int { x + y }")
	if len(doc.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(doc.Nodes))
	}
}

func TestDocumentManager_TracksErrors(t *testing.T) {
	dm := NewDocumentManager()
	doc := dm.Open("file:///test.nomi", "fn { broken }")
	if len(doc.Errors) == 0 {
		t.Error("expected parse errors for invalid input")
	}
}

func TestDocumentManager_AnalyzedClosedFile(t *testing.T) {
	dir := t.TempDir()
	mathFile := filepath.Join(dir, "math.nomi")
	os.WriteFile(mathFile, []byte("/// Doubles a number.\nfn double(n: Int): Int { n + n }"), 0644)

	dm := NewDocumentManager()
	snap := dm.Analyzed("file://" + mathFile)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("expected an analysis")
	}
	sym := snap.Analysis.ModuleScope.LookupLocal("double")
	if sym == nil {
		t.Fatal("expected double in module scope")
	}
	if sym.Kind != SymbolFunction {
		t.Errorf("expected SymbolFunction, got %v", sym.Kind)
	}
	if again := dm.Analyzed("file://" + mathFile); again != snap {
		t.Error("expected the second request to reuse the cached analysis")
	}

	// A change on disk is a new text: the cache entry no longer answers.
	os.WriteFile(mathFile, []byte("fn triple(n: Int): Int { n + n + n }"), 0644)
	dm.IndexFile("file://" + mathFile)
	snap = dm.Analyzed("file://" + mathFile)
	if snap.Analysis.ModuleScope.LookupLocal("triple") == nil {
		t.Error("expected the analysis of the new disk text")
	}
}

func TestDocumentManager_AnalyzedUsesOpenDoc(t *testing.T) {
	dir := t.TempDir()
	mathFile := filepath.Join(dir, "math.nomi")
	os.WriteFile(mathFile, []byte("fn disk(): Int { 1 }"), 0644)

	dm := NewDocumentManager()
	dm.Open("file://"+mathFile, "fn in_memory(): Int { 2 }")

	snap := dm.Analyzed("file://" + mathFile)
	if snap == nil || !snap.Open {
		t.Fatal("expected the open document's snapshot")
	}
	if snap.Analysis.ModuleScope.LookupLocal("in_memory") == nil {
		t.Fatal("expected in_memory from open document, not disk")
	}
}

// The closed-file cache keeps at most closedCacheSize analyses, dropping
// the least recently used.
func TestDocumentManager_ClosedCacheIsBounded(t *testing.T) {
	dir := t.TempDir()
	dm := NewDocumentManager()
	var uris []string
	for i := range closedCacheSize + 5 {
		path := filepath.Join(dir, fmt.Sprintf("f%d.nomi", i))
		os.WriteFile(path, []byte(fmt.Sprintf("fn f%d(): Int { %d }", i, i)), 0644)
		uris = append(uris, "file://"+path)
	}
	for _, uri := range uris {
		if dm.Analyzed(uri) == nil {
			t.Fatalf("no analysis of %s", uri)
		}
	}
	dm.mu.RLock()
	n := dm.closed.order.Len()
	_, oldest := dm.closed.byURI[uris[0]]
	_, newest := dm.closed.byURI[uris[len(uris)-1]]
	dm.mu.RUnlock()
	if n != closedCacheSize || oldest || !newest {
		t.Errorf("cache holds %d (want %d), oldest kept %v, newest kept %v", n, closedCacheSize, oldest, newest)
	}
}

func TestDocumentManager_IndexWorkspace(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(src), 0644)
	}
	write("math.nomi", "/// Doubles.\npub fn double(x: Int): Int { x * 2 }\nstruct Hidden {\n  n: Int\n}")
	write("main.nomi", "import math\nfn main() { math.double(21) }")
	for _, skipped := range []string{".hidden/ignore.nomi", "_scratch/a.nomi", "testdata/fixture.nomi", "node_modules/pkg/b.nomi", "lib/testdata/c.nomi"} {
		write(skipped, "fn skipped() {}")
	}
	write("lib/text.nomi", "pub fn shout(s: String): String { s }")

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	uris := dm.IndexWorkspace(context.Background())
	var rels []string
	for _, uri := range uris {
		rel, _ := filepath.Rel(dir, strings.TrimPrefix(uri, "file://"))
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	if got, want := strings.Join(rels, " "), "lib/text.nomi main.nomi math.nomi"; got != want {
		t.Fatalf("indexed %q, want %q", got, want)
	}
	for rel, want := range map[string]bool{"lib/text.nomi": true, "lib/testdata/c.nomi": false, "_scratch/a.nomi": false, "x.go": false} {
		if got := dm.Covers("file://" + filepath.Join(dir, rel)); got != want {
			t.Errorf("Covers(%s) = %v, want %v", rel, got, want)
		}
	}
	if dm.Covers("file://" + filepath.Join(filepath.Dir(dir), "elsewhere.nomi")) {
		t.Error("Covers a file outside the workspace root")
	}

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")
	if dm.Get(mathURI) != nil {
		t.Error("indexing must not open or analyze a file")
	}
	decls := dm.FileDecls(filepath.Join(dir, "math.nomi"))
	if len(decls) != 2 || decls[0].Name != "double" || !decls[0].Public || decls[0].Kind != SymbolFunction ||
		decls[0].Pos != (Pos{Line: 2, Col: 8}) || decls[0].Doc != "Doubles." || len(decls[0].Params) != 1 ||
		decls[1].Name != "Hidden" || decls[1].Public || decls[1].Kind != SymbolStruct {
		t.Errorf("unexpected decls %+v", decls)
	}
	deps := dm.ReverseDeps(mathURI)
	if len(deps) != 1 || deps[0] != mainURI {
		t.Errorf("expected main.nomi to depend on math.nomi, got %v", deps)
	}
}

func TestDocumentManager_PropagateChange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("pub fn double(x: Int): Int { x * 2 }"), 0644)
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("import math\nfn main() { math.double(21) }"), 0644)
	os.WriteFile(filepath.Join(dir, "app.nomi"), []byte("import main\nfn run() { 1 }"), 0644)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	dm.IndexWorkspace(context.Background())

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")
	appURI := "file://" + filepath.Join(dir, "app.nomi")

	// With main.nomi open, a change to math.nomi re-analyzes main.nomi
	// and names app.nomi, which imports main.nomi and is closed.
	dm.Open(mainURI, "import math\nfn main() { math.double(21) }")
	dm.UpdateImportEdges(mainURI)
	dm.Update(mathURI, "pub fn double(x: String): String { x + x }")
	open, closed := dm.PropagateChange(mathURI)
	if len(open) != 1 || open[0].URI != mainURI {
		t.Errorf("expected main.nomi re-analyzed, got %v", open)
	}
	if len(closed) != 1 || closed[0] != appURI {
		t.Errorf("expected app.nomi named as a closed dependent, got %v", closed)
	}
}

func TestDocumentManager_UpdateRebuildsImportEdges(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("pub fn double(x: Int): Int { x * 2 }"), 0644)
	os.WriteFile(filepath.Join(dir, "utils.nomi"), []byte("pub fn helper(): Int { 1 }"), 0644)
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("import math\nfn main() { math.double(21) }"), 0644)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	dm.IndexWorkspace(context.Background())

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	utilsURI := "file://" + filepath.Join(dir, "utils.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")

	// Initially main imports math
	deps := dm.ReverseDeps(mathURI)
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep on math, got %d", len(deps))
	}

	// Change main to import utils instead
	dm.Update(mainURI, "import utils: utils\nfn main() { utils.helper() }")
	dm.UpdateImportEdges(mainURI)

	// math should no longer have dependents
	deps = dm.ReverseDeps(mathURI)
	if len(deps) != 0 {
		t.Errorf("expected 0 deps on math after change, got %d: %v", len(deps), deps)
	}

	// utils should now have main as a dependent
	deps = dm.ReverseDeps(utilsURI)
	if len(deps) != 1 || deps[0] != mainURI {
		t.Errorf("expected [%s] deps on utils, got %v", mainURI, deps)
	}
}

func TestDocumentManager_CrossFileHover(t *testing.T) {
	dir := t.TempDir()
	mathFile := filepath.Join(dir, "math.nomi")
	mainFile := filepath.Join(dir, "main.nomi")

	os.WriteFile(mathFile, []byte("/// Doubles a number.\npub fn double(n: Int): Int { n + n }"), 0644)
	os.WriteFile(mainFile, []byte("import math\nfn main() { math.double(21) }"), 0644)

	dm := NewDocumentManager()
	mainURI := "file://" + mainFile
	doc := dm.Open(mainURI, "import math\nfn main() { math.double(21) }")

	var doubleSym *Symbol
	for _, sym := range doc.Analysis.References {
		if sym.Name == "double" {
			doubleSym = sym
			break
		}
	}
	if doubleSym == nil {
		t.Fatal("double not resolved — cross-file loader not working")
	}
	if doubleSym.Doc != "Doubles a number." {
		t.Errorf("expected doc from math.nomi, got %q", doubleSym.Doc)
	}
}

func TestDocumentManager_CrossFileDiagnostics(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("pub fn double(x: Int): Int { x * 2 }"), 0644)
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("import math\nfn main() { math.double(21) }"), 0644)

	lib := NewScope(nil)
	dm := NewDocumentManager()
	dm.SetStdlib(lib, nil, nil)
	dm.SetWorkspaceRoot(dir)
	dm.IndexWorkspace(context.Background())

	mathURI := "file://" + filepath.Join(dir, "math.nomi")
	mainURI := "file://" + filepath.Join(dir, "main.nomi")

	// Change math.nomi to have a parse error
	dm.Update(mathURI, "pub fn double(x: Int): Int { x * ")

	// main.nomi is closed: propagation names it for a diagnostics pass
	// instead of analyzing and keeping it.
	open, closed := dm.PropagateChange(mathURI)
	if len(open) != 0 || len(closed) != 1 || closed[0] != mainURI {
		t.Errorf("expected main.nomi as the one closed dependent, got open %v closed %v", open, closed)
	}
	if snap := dm.AnalyzeClosed(mainURI); snap == nil || snap.Analysis == nil {
		t.Error("expected a transient analysis of main.nomi")
	}
	if dm.Get(mainURI) != nil {
		t.Error("AnalyzeClosed must not keep the file")
	}
}

// Project-root discovery walks upward from the file looking for nomi.toml
// or main.nomi, falling back to the file's own directory. Until this
// rule landed the LSP used the file's immediate parent unconditionally,
// breaking spec §26's nested-directory file-mode layouts.

func TestFindProjectRoot_FindsMainNomiInParent(t *testing.T) {
	tmp := t.TempDir()
	// project layout:
	//   <tmp>/main.nomi    ← marker
	//   <tmp>/sub/foo.nomi ← file under analysis
	if err := os.MkdirAll(filepath.Join(tmp, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	foo := filepath.Join(tmp, "sub", "foo.nomi")
	if err := os.WriteFile(foo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(foo, "")
	if root != tmp {
		t.Errorf("expected project root %q, got %q", tmp, root)
	}
}

// A nomi.toml anywhere above the file wins over a closer main.nomi: in
// project mode every file of the module resolves from the manifest's
// directory, including an entry in a subdirectory.
func TestFindProjectRoot_NomiTomlBeatsACloserMainNomi(t *testing.T) {
	tmp := t.TempDir()
	// <tmp>/nomi.toml          ← project marker
	// <tmp>/inner/main.nomi    ← file-mode marker (closer)
	// <tmp>/inner/sub/foo.nomi ← file under analysis
	if err := os.MkdirAll(filepath.Join(tmp, "inner", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "nomi.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	innerMain := filepath.Join(tmp, "inner", "main.nomi")
	if err := os.WriteFile(innerMain, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	foo := filepath.Join(tmp, "inner", "sub", "foo.nomi")
	if err := os.WriteFile(foo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(foo, "")
	if root != tmp {
		t.Errorf("expected the nomi.toml directory %q, got %q", tmp, root)
	}
}

func TestFindProjectRoot_FindsNomiToml(t *testing.T) {
	tmp := t.TempDir()
	// <tmp>/nomi.toml      ← marker
	// <tmp>/src/foo.nomi   ← file under analysis (no main.nomi)
	if err := os.MkdirAll(filepath.Join(tmp, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "nomi.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	foo := filepath.Join(tmp, "src", "foo.nomi")
	if err := os.WriteFile(foo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(foo, "")
	if root != tmp {
		t.Errorf("expected nomi.toml dir %q, got %q", tmp, root)
	}
}

func TestFindProjectRoot_FallsBackToFileDirWhenNoMarker(t *testing.T) {
	tmp := t.TempDir()
	// <tmp>/foo.nomi  ← lone file, no markers anywhere up the tree
	foo := filepath.Join(tmp, "foo.nomi")
	if err := os.WriteFile(foo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Pass tmp as the workspace root so the walk doesn't escape it
	// looking for unrelated markers higher in the filesystem.
	root := ProjectRoot(foo, tmp)
	if root != tmp {
		t.Errorf("expected fallback to file's dir %q, got %q", tmp, root)
	}
}

// Cross-module impl conformance: when one file declares
// `impl Iface for X` and another file constructs a struct whose field is
// typed as Iface but assigned an X value, the type checker must see the
// impl from the imported file. Without this, the checker rejects
// otherwise-valid assignments because impls are stored per-file.
func TestDocumentManager_CrossModuleImplConformance(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	// Project layout:
	//   <tmp>/main.nomi          ← project root marker
	//   <tmp>/log.nomi           ← interface + impl
	//   <tmp>/effects.nomi       ← struct with interface-typed field;
	//                              constructs ProdLogger and assigns
	mustWrite("main.nomi", "import effects: effects\n\nfn main() { effects.prod() }\n")
	mustWrite("log.nomi", `pub interface Logger {
  fn log(value: self, msg: String): Unit
}

pub type ProdLogger

impl Logger for ProdLogger {
  fn log(_logger: ProdLogger, _msg: String): Unit { Unit }
}
`)
	effectsPath := mustWrite("effects.nomi", `import log.{Logger, ProdLogger}

pub struct Effects {
  logger: Logger
}

pub fn prod(): Effects { Effects{logger: ProdLogger{}} }
`)

	dm := NewDocumentManager()
	src, _ := os.ReadFile(effectsPath)
	dm.Open("file://"+effectsPath, string(src))

	doc := dm.Get("file://" + effectsPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error from cross-module impl: %s", e.Message)
		}
	}
}

func TestDocumentManager_TestProgramSupportFileUsesProgramRoot(t *testing.T) {
	tmp := t.TempDir()
	category := filepath.Join(tmp, "tests", "15-app-and-defer")
	mustWrite := func(rel, content string) string {
		full := filepath.Join(category, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("effects/app/dev.nomi", `import app.DemoApp

pub fn build(): DemoApp {
  DemoApp{label: "dev"}
}
`)
	appPath := mustWrite("effects/app.nomi", `import app/dev

pub struct DemoApp {
  label: String
}

pub fn load(_name: String): DemoApp {
  dev.build()
}

`)
	mustWrite("effects/effects_test.nomi", `import app

test "app support resolves from the test program root" {
  assert app.load("dev").label == "dev"
}
`)

	dm := NewDocumentManager()
	dm.SetWorkspaceRoot(tmp)
	src, _ := os.ReadFile(appPath)
	dm.Open("file://"+appPath, string(src))

	doc := dm.Get("file://" + appPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error in test_program support file: %s", e.Message)
		}
	}
}

func TestFindProjectRoot_TestProgramDirectoryBeatsCategoryRoot(t *testing.T) {
	tmp := t.TempDir()
	category := filepath.Join(tmp, "tests", "10-generics-and-type-wrappers")
	program := filepath.Join(category, "opaque_types")
	if err := os.MkdirAll(program, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(category, "root_test.nomi"), []byte("test \"root\" { assert True }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "opaque_types_test.nomi"), []byte("import email: Email\n"), 0644); err != nil {
		t.Fatal(err)
	}
	email := filepath.Join(program, "email.nomi")
	if err := os.WriteFile(email, []byte("pub type Email String\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(email, tmp)
	if root != program {
		t.Fatalf("expected directory-shaped test program root %q, got %q", program, root)
	}
}

func TestFindProjectRoot_TestProgramAppMarkerBeatsCategoryRoot(t *testing.T) {
	tmp := t.TempDir()
	category := filepath.Join(tmp, "tests", "18-ffi-and-dynamic")
	program := filepath.Join(category, "tagged_ffi_app")
	if err := os.MkdirAll(program, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "main.nomi"), []byte("fn main() { Unit }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(program, "helper.nomi")
	if err := os.WriteFile(helper, []byte("pub fn ok(): Bool { True }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(helper, tmp)
	if root != program {
		t.Fatalf("expected app-shaped test program root %q, got %q", program, root)
	}
}

func TestFindProjectRoot_TestFileOutsideTestProgramsUsesProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "pkg")
	tests := filepath.Join(project, "tests")
	if err := os.MkdirAll(tests, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "nomi.toml"), []byte("[module]\nname = \"pkg\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(tests, "feature_test.nomi")
	if err := os.WriteFile(fixture, []byte("test \"feature\" { assert True }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := ProjectRoot(fixture, tmp)
	if root != project {
		t.Fatalf("expected project root %q, got %q", project, root)
	}
}

// End-to-end check: opening a file from a sub-directory of a project
// should resolve cross-file imports against the project root, not the
// file's immediate parent. Without the walk-upward rule the analysis
// for sub/foo.nomi would fail to find types.nomi one directory up.
func TestDocumentManager_SubdirectoryImportResolvesAgainstProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	// Project layout:
	//   <tmp>/main.nomi          ← project root marker; entry point
	//   <tmp>/types.nomi         ← shared types
	//   <tmp>/sub/uses_types.nomi ← imports from project root
	//
	// Type chosen as `Settings` (not `Config`) — Config is the project's
	// flat root struct per spec §26/§27; declaring one without a
	// matching `fn boot(): Config` is a separate analyzer error.
	mustWrite("main.nomi", "import types\n\nfn main() { io.inspect(types.Settings{port: 80}.port) }\n")
	mustWrite("types.nomi", "pub struct Settings { port: Int }\n")
	subFoo := mustWrite("sub/uses_types.nomi", "import types.{Settings}\n\npub fn handle(c: Settings): Int { c.port }\n")

	dm := NewDocumentManager()
	dm.Open("file://"+subFoo, "import types.{Settings}\n\npub fn handle(c: Settings): Int { c.port }\n")

	doc := dm.Get("file://" + subFoo)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error in sub-directory file: %s", e.Message)
		}
	}
}

func TestFindProjectRoot_StopsAtWorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	// <tmp>/main.nomi             ← marker that walk would reach
	// <tmp>/workspace/sub/foo.nomi ← file; workspace root is <tmp>/workspace
	if err := os.MkdirAll(filepath.Join(tmp, "workspace", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	foo := filepath.Join(tmp, "workspace", "sub", "foo.nomi")
	if err := os.WriteFile(foo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Workspace bound stops the walk at <tmp>/workspace; the outer
	// main.nomi must not be picked up because it's above the workspace.
	workspace := filepath.Join(tmp, "workspace")
	root := ProjectRoot(foo, workspace)
	wantFallback := filepath.Join(tmp, "workspace", "sub")
	if root != wantFallback {
		t.Errorf("expected fallback %q (walk bounded at workspace), got %q", wantFallback, root)
	}
}

// TestDocumentManager_TypeLevelCycleAcrossFiles lives in
// document_project_test.go (package analysis_test) — it needs the
// stdlib loaded so primitives like Maybe resolve, and stdlib imports
// analysis (cycle), so it can't sit in this in-package test file.

// TestIndexFile_LeavesOpenDocAlone pins that a watcher event for an open
// file never replaces the editor's text with the disk's.
func TestIndexFile_LeavesOpenDocAlone(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "foo.nomi")
	if err := os.WriteFile(filePath, []byte("fn disk(): Int { 1 }"), 0644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + filePath

	dm := NewDocumentManager()
	editorContent := "fn editor(): Int { 2 }"
	openDoc := dm.Open(uri, editorContent)
	if dm.IndexFile(uri) {
		t.Error("IndexFile indexed an open document")
	}
	post := dm.Get(uri)
	if post != openDoc || post.Content != editorContent {
		t.Errorf("IndexFile touched the open document; content %q", post.Content)
	}
}

// The LSP document path must keep source impl blocks on the doc's OWN node
// slice: every branch of analyze() runs CheckTypes / MarkTailCalls over
// doc.Nodes, and the build pipelines' internally-extended slices never escape.
func TestDocumentManager_SourceImplBlocksStayOnDocumentNodes(t *testing.T) {
	dm := NewDocumentManager()
	src := `pub struct Dog {
  name: String
}

pub fn loud(d: Dog): String {
  d.name
}


impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name
  }
}

pub interface Speech {
  fn speak(animal: self): String
}

`
	dm.Open("file:///test_nested.nomi", src)

	doc := dm.Get("file:///test_nested.nomi")
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}
	// doc.Nodes must hold the source impl blocks beside the struct decl.
	var implBlocks int
	for _, n := range doc.Nodes {
		switch d := n.(type) {
		case *ast.StructDef:
			if len(d.Items) != 0 {
				t.Errorf("expected no struct body function items on doc.Nodes, got %d", len(d.Items))
			}
		case *ast.ImplBlock:
			implBlocks++
			if d.Receiver == nil || TypeExprBaseName(d.Receiver) != "Dog" {
				t.Errorf("expected lowered impl block receiver Dog, got %v", d.Receiver)
			}
		}
	}
	if implBlocks != 1 {
		t.Errorf("expected 1 impl block in doc.Nodes (Speech), got %d", implBlocks)
	}
	// The impl method must be registered for the receiver type.
	if doc.Analysis.TypeMethods["Dog"]["speak"] == nil {
		t.Error("expected impl block method 'speak' in TypeMethods[Dog]")
	}
}
