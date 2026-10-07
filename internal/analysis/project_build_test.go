package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

func TestBuildProject_ResolvesTypeLevelCycles(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mainPath := mustWrite("main.nomi", "import {\n  a\n  b\n}\n\nfn main() {\n  x: Maybe<a.A> = None\n  y: Maybe<b.B> = None\n  Unit\n}\n")
	mustWrite("a.nomi", `import b.{B}

pub struct A {
  partner: Maybe<B>
}
`)
	mustWrite("b.nomi", `import a.{A}

pub struct B {
  partner: Maybe<A>
}
`)

	// Parse the entry file
	data, _ := os.ReadFile(mainPath)
	tokens := lexer.Lex(string(data))
	entryNodes, _ := parser.ParseWithRecovery(tokens)

	// Build a loader matching what DocumentManager.makeLoader does
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		fileTokens := lexer.Lex(string(fileData))
		nodes, _ := parser.ParseWithRecovery(fileTokens)
		return nodes, nil
	}

	// Use stdlib for primitives + modules
	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	for _, e := range fa.TypeErrors {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		if e.Message != "" {
			t.Errorf("unexpected type error in cyclic-types project: %s", e.Message)
		}
	}
}

// TestBuildProject_PropagatesCrossFileImpls exercises Sweep B-impls:
// when one file declares an impl and a sibling depends on the
// conformance through a struct field typed as the interface, both
// files must end up with the impl propagated into their merged Impls
// table — regardless of the (random) order Sweep B-ann visited them.
//
// Topology:
//
//	log.nomi      — declares interface Iface, type ProdImpl, and
//	                impl Iface for ProdImpl.
//	effects.nomi  — imports log.{Iface, ProdImpl} and exposes a
//	                Holder struct whose `handler` field is typed as
//	                Iface. The body `Holder{handler: ProdImpl{}}`
//	                inside effects.make() is the conformance site
//	                that needs log's impl visible in effects.fa.
//	main.nomi     — imports both, constructs a Holder via the
//	                cross-file impl, and asserts the impl is in
//	                main.fa.Impls.
//
// The assertion is on the merged table directly (rather than purely
// through CheckTypes) so the test is explicit about *what* the new
// sweep produces, and surfaces the bug regardless of which file
// CheckTypes happens to run on.
func TestBuildProject_PropagatesCrossFileImpls(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mainPath := mustWrite("main.nomi", `import {
  effects.{self, Holder}
  log.{Iface, ProdImpl}
}

fn main() {
  h: Iface = ProdImpl{}
  e: Holder = effects.make()
  Unit
}
`)
	mustWrite("log.nomi", `pub interface Iface {
  fn op(value: self): Unit
}

pub type ProdImpl

impl Iface for ProdImpl {
  fn op(_value: ProdImpl): Unit { Unit }
}
`)
	mustWrite("effects.nomi", `import log.{Iface, ProdImpl}

pub struct Holder {
  handler: Iface
}

pub fn make(): Holder {
  Holder{handler: ProdImpl{}}
}

`)

	data, _ := os.ReadFile(mainPath)
	tokens := lexer.Lex(string(data))
	entryNodes, _ := parser.ParseWithRecovery(tokens)

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		fileTokens := lexer.Lex(string(fileData))
		nodes, _ := parser.ParseWithRecovery(fileTokens)
		return nodes, nil
	}

	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	// Direct assertion on the merged Impls table: main.fa.Impls must
	// contain ProdImpl→Iface, propagated from log.nomi. The legacy
	// merge-during-resolveImport path could miss this when
	// log.nomi's annotations hadn't run yet at the moment effects.nomi
	// processed its log import (Go map iteration order is random).
	if fa.Impls == nil || !fa.Impls["ProdImpl"]["Iface"] {
		t.Errorf("expected main.fa.Impls to contain ProdImpl as Iface (propagated from log.nomi), got: %v", fa.Impls)
	}

	// And, since CheckTypes is the consumer that turns Impls into
	// real conformance verdicts, also assert no type errors at the
	// entry file's CheckTypes pass.
	checkErrs := analysis.CheckTypes(fa, entryNodes)

	allErrs := append([]analysis.TypeError{}, fa.TypeErrors...)
	allErrs = append(allErrs, checkErrs...)
	for _, e := range allErrs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		if e.Message != "" {
			t.Errorf("unexpected type error in cross-file-impl project: %s", e.Message)
		}
	}
}

// buildProjectExpectingErrors runs the real BuildProject pipeline over an
// entry file plus sibling modules and RETURNS the accumulated TypeErrors
// (entry FA + entry CheckTypes) instead of failing on them — the
// counterpart to buildProjectManifest, which t.Fatalf's on any
// diagnostic and so can't be used to assert that an error fired.
func buildProjectExpectingErrors(t *testing.T, entry string, siblings map[string]string) []analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte(entry), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	for name, content := range siblings {
		full := filepath.Join(tmp, name+".nomi")
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	data, _ := os.ReadFile(mainPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, entryNodes)...)
	return errs
}

func assertErrorContains(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	t.Errorf("expected an error containing %q, got %d errors: %v", substr, len(errs), errs)
}

func TestBuildProject_PrivateModuleMemberHiddenAcrossFiles(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
import api.{self, Widget}

fn demo(): String {
  w: Widget = api.make("Ada")
  api.secret(w)
}
`, map[string]string{
		"api": `
pub struct Widget {
  name: String
}

pub fn make(name: String): Widget {
  Widget{name}
}

fn secret(w: Widget): String {
  w.name
}
`,
	})

	assertErrorContains(t, errs, "file 'api' has no member 'secret'")
}

func hasTypeErrorContaining(errs []analysis.TypeError, substrs ...string) bool {
	for _, e := range errs {
		match := true
		for _, substr := range substrs {
			if !strings.Contains(e.Message, substr) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestImplCollision_CrossModuleRejected exercises the wired-in collision
// check through the real BuildProject pipeline: two sibling modules each
// declare `impl Display for Int`, i.e. one (Iface, Type) pair backed by
// two distinct impl blocks across two modules. The build must reject
// it. (`Int` is a primitive, visible everywhere — no re-export wiring
// needed to put the same receiver type in both modules.)
func TestImplCollision_CrossModuleRejected(t *testing.T) {
	siblings := map[string]string{
		"widgets": "impl Display for Int {\n  fn to_string(_n: self): String { \"widgets\" }\n}\n",
		"legacy":  "impl Display for Int {\n  fn to_string(_n: self): String { \"legacy\" }\n}\n",
	}
	entry := "import {\n  std/io\n  widgets\n  legacy\n}\n\nfn main() { io.print(\"x\") }\n"
	errs := buildProjectExpectingErrors(t, entry, siblings)
	assertErrorContains(t, errs, "duplicate impl")
	assertErrorContains(t, errs, "widgets")
	assertErrorContains(t, errs, "legacy")
}

// TestImplCollision_SameFileRejected confirms the check catches a
// duplicate (Iface, Type) pair declared twice within ONE file, not just
// across modules — both impl blocks land in the same module's slice
// of IfaceMethodImpls and detectImplCollisions groups them by receiver.
// The collision check is BuildProject-scoped (BuildFileWithStdlib never
// assembles IfaceMethodImpls), so this drives the entry file through the
// real BuildProject pipeline — the path that does assemble the index.
func TestImplCollision_SameFileRejected(t *testing.T) {
	entry := "impl Display for Int {\n" +
		"  fn to_string(_n: self): String { \"first\" }\n" +
		"}\n\n" +
		"impl Display for Int {\n" +
		"  fn to_string(_n: self): String { \"second\" }\n" +
		"}\n\n" +
		"fn main() {}\n"
	errs := buildProjectExpectingErrors(t, entry, nil)
	assertErrorContains(t, errs, "duplicate impl")
	assertErrorContains(t, errs, "Display")
	assertErrorContains(t, errs, "Int")
}

// TestBuildProject_MalformedManifestSurfacesAsTypeError confirms that
// when nomi.toml is present but malformed, BuildProjectWithCache surfaces
// the parse failure as a TypeError on the entry FA rather than silently
// dropping it. The plumbing path (DiscoverProject → BuildProjectWithCache)
// has only one observable side effect for the error case; the success
// case is covered by discovery_test's TestDiscoverProject_LoadsManifest.
func TestBuildProject_MalformedManifestSurfacesAsTypeError(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
	}
	mustWrite("nomi.toml", "[module\nname = \"todo\"\n")
	mustWrite("main.nomi", "fn main() { Unit }\n")

	data, _ := os.ReadFile(filepath.Join(tmp, "main.nomi"))
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis even with malformed nomi.toml")
	}
	found := false
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "nomi.toml") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a TypeError mentioning nomi.toml, got %d errors: %v",
			len(fa.TypeErrors), fa.TypeErrors)
	}
}

// writeCratePackagingExample lays out a two-module tree on disk: a `todo/`
// module whose `go.mod` replaces a sibling `stringkit/` module with its local
// path.
// Returns the path of the entry file the caller wants to build from.
//
// The shared helper keeps each test focused on its assertion; the
// disk shape is identical across the cross-module tests.
func writeCratePackagingExample(t *testing.T, todoMain string, stringkitPad string, extraTodoFiles map[string]string) (todoRoot, stringkitRoot, entryPath string) {
	t.Helper()
	tmp := t.TempDir()
	todoRoot = filepath.Join(tmp, "todo")
	stringkitRoot = filepath.Join(tmp, "stringkit")
	mustWrite := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	// stringkit module
	mustWrite(filepath.Join(stringkitRoot, "nomi.toml"), `[module]
name = "stringkit"
`)
	mustWrite(filepath.Join(stringkitRoot, "go.mod"), "module stringkit\n\ngo 1.22\n")
	mustWrite(filepath.Join(stringkitRoot, "pad.nomi"), stringkitPad)

	// todo module
	mustWrite(filepath.Join(todoRoot, "nomi.toml"), `[module]
name = "todo"
entry_points = ["main"]
`)
	mustWrite(filepath.Join(todoRoot, "go.mod"), `module todo

go 1.22

require stringkit v0.0.0

replace stringkit => ../stringkit
`)
	entryPath = filepath.Join(todoRoot, "main.nomi")
	mustWrite(entryPath, todoMain)

	for name, content := range extraTodoFiles {
		mustWrite(filepath.Join(todoRoot, name), content)
	}
	return todoRoot, stringkitRoot, entryPath
}

// TestBuildProject_CrossModuleImportResolves is the happy-path test
// for cross-module imports: an `import stringkit/pad` in todo/main.nomi must resolve
// against ../stringkit/pad.nomi (per todo/go.mod's replace directive)
// and bring pad.nomi's symbols into the import-time scope of main.nomi.
//
// The assertion is on the side-effect proof of resolution: pad.nomi
// declares `pad_left(s, w, p)` and main.nomi calls it. If
// resolution worked, no "module not found" or "undefined name" type
// errors should fire on the entry file.
func TestBuildProject_CrossModuleImportResolves(t *testing.T) {
	stringkitPad := `pub fn pad_left(s: String, _width: Int, _pad: String): String {
  s
}
`
	todoMain := `import {
  stringkit/pad.pad_left
}

fn main() {
  _ = pad_left("hi", 4, " ")
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa, siblingFAs, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	checkErrs := analysis.CheckTypes(fa, entryNodes)
	allErrs := append([]analysis.TypeError{}, fa.TypeErrors...)
	allErrs = append(allErrs, checkErrs...)
	for _, e := range allErrs {
		if e.Message == "" {
			continue
		}
		t.Errorf("unexpected type error: %s", e.Message)
	}

	// Sanity: the discovered project should include stringkit/pad's
	// nodes. cache is keyed by import-form module path.
	if _, ok := siblingFAs["stringkit/pad"]; !ok {
		var keys []string
		for k := range siblingFAs {
			keys = append(keys, k)
		}
		t.Errorf("expected siblingFAs to contain key %q, got keys: %v", "stringkit/pad", keys)
	}
}

// TestBuildProject_CrossModuleInternalRejected confirms that the
// internal/ access check actively rejects cross-module imports into
// another module's internal/ subtree; the module index is what
// distinguishes intra- from cross-module. A cross-
// module import of an internal/ path must produce a type error.
func TestBuildProject_CrossModuleInternalRejected(t *testing.T) {
	// stringkit exposes an internal helper; todo tries to reach across
	// the module boundary into it.
	stringkitPad := `pub fn pad_left(s: String, _width: Int, _pad: String): String {
  s
}
`
	todoMain := `import {
  stringkit/internal/secret: secret
}

fn main() {
  Unit
}
`
	todoRoot, stringkitRoot, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	// Plant the internal file in stringkit so the loader has something
	// to find; the access check should reject before resolution
	// matters, but a present file rules out "file not found" as the
	// reason for any non-error.
	internalPath := filepath.Join(stringkitRoot, "internal", "secret.nomi")
	if err := os.MkdirAll(filepath.Dir(internalPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(internalPath, []byte("module secret\n\npub fn s(): Int { 0 }\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	found := false
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "internal/") && strings.Contains(e.Message, "unreachable") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an `internal/ paths are unreachable` type error, got %d: %v",
			len(fa.TypeErrors), fa.TypeErrors)
	}
}

// TestBuildProject_SelfNameReferenceResolves confirms that referring to
// your own module by its short name (`import todo/helper` from within
// todo/) is equivalent to a bare `helper` import — the cross-module
// path syntax that loops back into the current module must resolve
// against the current project root and reach helper.nomi.
//
// The assertion is direct: helper.nomi's nodes must appear in the
// project's sibling cache under key "helper" (the rest-of-path after
// the current module's name is stripped). Without the self-name strip in
// resolveImport, the lookup would search
// <root>/todo/helper.nomi (which doesn't exist) and silently drop the
// import.
func TestBuildProject_SelfNameReferenceResolves(t *testing.T) {
	stringkitPad := `pub fn pad_left(s: String, _width: Int, _pad: String): String {
  s
}
`
	todoMain := `import {
  todo/helper
}

fn main() {
  _ = helper.greet()
  Unit
}
`
	extra := map[string]string{
		"helper.nomi": "pub fn greet(): String { \"hi\" }\n",
	}
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, extra)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa, siblingFAs, siblingNodes := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	if _, ok := siblingNodes["helper"]; !ok {
		var keys []string
		for k := range siblingNodes {
			keys = append(keys, k)
		}
		t.Errorf("expected siblingNodes to contain key %q (self-name reference loops to current module root), got keys: %v", "helper", keys)
	}
	if _, ok := siblingFAs["helper"]; !ok {
		t.Errorf("expected siblingFAs to contain key %q", "helper")
	}
	checkErrs := analysis.CheckTypes(fa, entryNodes)
	allErrs := append([]analysis.TypeError{}, fa.TypeErrors...)
	allErrs = append(allErrs, checkErrs...)
	for _, e := range allErrs {
		if e.Message == "" {
			continue
		}
		t.Errorf("unexpected type error: %s", e.Message)
	}
}

// TestOrphanImpl_CrossModuleRejected exercises the orphan-rule check
// wired into BuildProjectWithCache: the todo entry
// module declares `impl Display for Pad { fn to_string(p: pad.Pad) }` — Display
// belongs to stdlib, Pad belongs to the stringkit sibling module, so
// neither side anchors the impl in todo. The build must surface a
// `orphan impl` TypeError on the entry FA.
//
// Drives BuildProjectWithCache through the same writeCratePackagingExample
// helper the cross-module import tests use, so the assertion exercises
// the full wiring: discovery → declaringModuleIndex over filesByKey →
// implModuleOf via moduleNameFromKey → detectOrphanImpls.
func TestOrphanImpl_CrossModuleRejected(t *testing.T) {
	stringkitPad := `pub struct Pad {
  width: Int
}
`
	todoMain := `import {
  std/display.{Display}
  stringkit/pad.{Pad}
}

impl Display for Pad {
  fn to_string(_p: Pad): String { "pad" }
}

fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	// Production-style loader: serves stdlib from the embedded FS first,
	// disk under projectRoot for everything else. Post-stdlib-as-package,
	// the orphan-rule check derives stdlib name buckets from
	// declaringModuleIndex's walk of discovery-loaded stdlib files (not
	// from package globals anymore), so a disk-only loader would leave
	// `Display` unresolved and silently skip the orphan diagnostic.
	loader := std.MakeLoader()

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	var orphanErr *analysis.TypeError
	for i := range fa.TypeErrors {
		e := &fa.TypeErrors[i]
		if strings.Contains(e.Message, "orphan impl") {
			orphanErr = e
			break
		}
	}
	if orphanErr == nil {
		t.Fatalf("expected an `orphan impl` TypeError, got %d errors: %v",
			len(fa.TypeErrors), fa.TypeErrors)
	}
	for _, substr := range []string{"Display", "Pad", "todo"} {
		if !strings.Contains(orphanErr.Message, substr) {
			t.Errorf("orphan error missing %q: %s", substr, orphanErr.Message)
		}
	}
}

func TestBuildProject_AllowsImplBlockForCurrentPackageType(t *testing.T) {
	stringkitPad := `pub struct Pad {
  width: Int
}
`
	todoMain := `pub interface Label {
  fn label(value: self): String
}

pub struct Thing {
  name: String
}

impl Label for Thing {
  fn label(value: Thing): String { value.name }
}

fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, std.MakeLoader(),
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	if len(fa.TypeErrors) != 0 {
		t.Fatalf("local type impl block should be allowed, got %v", fa.TypeErrors)
	}
}

func TestBuildProject_AllowsConstrainedGenericImplBlockForCurrentPackageType(t *testing.T) {
	stringkitPad := `pub struct Pad {
  width: Int
}
`
	todoMain := `pub interface StepLabel {
  fn label(value: self): String
}

pub struct Box<T> {
  value: T
}

impl StepLabel for Box<T> where T: Display {
  fn label(value: Box<T>): String { Display.to_string(value.value) }
}

fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, std.MakeLoader(),
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	if len(fa.TypeErrors) != 0 {
		t.Fatalf("local constrained generic impl block should be allowed, got %v", fa.TypeErrors)
	}
}

func TestBuildProject_RejectsDuplicateModuleFunctions(t *testing.T) {
	todoMain := `pub struct Thing {
	  name: String
	}

	pub fn name(value: Thing): String { value.name }

	pub fn name(_value: Thing, fallback: String): String { fallback }



fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, "", nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, std.MakeLoader(),
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	if !hasTypeErrorContaining(fa.TypeErrors, "'name' is already defined") {
		t.Fatalf("expected duplicate module function error, got %v", fa.TypeErrors)
	}
}

func TestBuildProject_AllowsForeignImplBlockForExternalReceiverType(t *testing.T) {
	stringkitPad := `pub struct Pad {
  width: Int
}
`
	todoMain := `import stringkit/pad: Pad

pub interface Label {
  fn label(value: self): String
}

impl Label for Pad {
  fn label(_value: Pad): String { "pad" }
}

fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, std.MakeLoader(),
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	checkErrs := analysis.CheckTypes(fa, entryNodes)
	allErrs := append([]analysis.TypeError{}, fa.TypeErrors...)
	allErrs = append(allErrs, checkErrs...)
	for _, e := range allErrs {
		if e.Message == "" {
			continue
		}
		t.Errorf("unexpected type error: %s", e.Message)
	}
}

// TestBuildProject_UnknownCrossModuleErrorsCleanly confirms that when
// no module index entry matches the import head, the build doesn't
// crash; it produces a normal "module not found"-style error path.
// (The single-file flow handles this too — the test proves the
// cross-module wiring doesn't break it.)
func TestBuildProject_UnknownCrossModuleErrorsCleanly(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}
	// No nomi.toml, no go.mod — single-file mode. Importing a path
	// that doesn't exist on disk should produce a clean failure rather
	// than panic on module-index lookup.
	mainPath := mustWrite("main.nomi", "import { ghost/widget }\n\nfn main() {}\n")
	data, _ := os.ReadFile(mainPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}
	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	// No-op verification: we don't assert a specific error message
	// here. The point is "doesn't crash". The build either resolves
	// the import or emits an analyzer error; either is fine.
}

// TestDiscovery_WalksStdlibAfterCutover pins that the stdlib is an
// ordinary module to discovery: a trivial user program (no explicit
// stdlib imports) still produces a sibling FA map containing stdlib
// keys, because the auto-prepended prelude chain feeds them through
// the regular discovery pipeline.
//
// The prelude chain `import std/prelude.{...}` (auto-
// prepended into every non-stdlib file) walks every stdlib file
// prelude re-exports — at least `std/int`, `std/display`, etc.
//
// The assertion floor is "at least one std/* key present". A stronger
// "every prelude re-export reachable" claim is appealing but couples
// the test to the current prelude.nomi enumeration; adding a single
// std/* key check keeps the test focused on the discovery-skip-
// removal property and not on what prelude.nomi happens to ship.
func TestDiscovery_WalksStdlibAfterCutover(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte("fn main() { Unit }\n"), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	data, _ := os.ReadFile(mainPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	_, siblingFAs, _ := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, tmp, loader,
	)
	var stdKeys []string
	for k := range siblingFAs {
		if strings.HasPrefix(k, "std/") {
			stdKeys = append(stdKeys, k)
		}
	}
	if len(stdKeys) == 0 {
		var allKeys []string
		for k := range siblingFAs {
			allKeys = append(allKeys, k)
		}
		t.Fatalf("expected at least one std/* key in siblingFAs (auto-prepended prelude should pull stdlib into discovery), got keys: %v",
			allKeys)
	}
}

func TestBuildProject_AllowsLocalAppFileAlongsideStdApp(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("main.nomi", `import {
  app: EffectsApp
}

fn boot(): EffectsApp {
  EffectsApp{context: Context.root(), }
}
`)
	mustWrite("app.nomi", `pub struct EffectsApp {}

`)

	data, _ := os.ReadFile(filepath.Join(tmp, "main.nomi"))
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()

	entryFA, siblingFAs, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	appFA, ok := siblingFAs["app"]
	if !ok || appFA == nil {
		var keys []string
		for k := range siblingFAs {
			keys = append(keys, k)
		}
		t.Fatalf("expected siblingFAs[\"app\"] to exist for app.nomi; got keys %v", keys)
	}
	if entryFA == nil {
		t.Fatal("expected non-nil entry FA")
	}
	if entryFA.ModuleScope.Lookup("EffectsApp") == nil {
		t.Fatalf("expected selective import app: EffectsApp to bind EffectsApp in main scope")
	}
	noCollision := func(label string, errs []analysis.TypeError) {
		for _, e := range errs {
			if strings.Contains(e.Message, "module short-name collision") {
				t.Fatalf("unexpected module short-name collision on %s: %s", label, e.Message)
			}
		}
	}
	noCollision("entry", entryFA.TypeErrors)
	noCollision("app.nomi", appFA.TypeErrors)
}

// TestOrphanRule_CacheFoldBranch_PreludeImplicitUse covers the
// prelude-implicit path: the other tests that exercise the orphan rule against
// stdlib types do so via explicit `import std/display.{Display}`,
// which lands the stdlib FA in `all` (discovery walks it from the
// user's import line). The cache branch of `filesByKey`'s fold in
// project_build.go's BuildProjectWithCache is exercised only when
// stdlib reaches the build via on-demand loading during sweeps
// (specifically, the auto-prepended prelude chain's drill-through).
//
// This test deletes the explicit `import std/display.{Display}` and
// uses `Display` purely via the prelude. The entry FA's orphan
// diagnostic must still fire — proof that the cache-side fold makes
// declaringModuleIndex see Display even when the user never wrote a
// `std/display` import.
//
// The test passes `nil, nil` for `primitives, modules` to force the
// post-cutover path: `b.resolveStdlibImport` fast-pathing via
// b.modules["display"] would otherwise short-circuit the cache write
// (it returns a pre-built scope from std.Load() without going
// through resolveImport). With nil modules, prelude imports take
// the slow resolveImport path which DOES write to cache — exactly
// the path the `for key, fa := range cache` branch of the fold
// covers.
//
// A future refactor that drops the cache branch of the fold (because
// "all already has everything") would silently lose this coverage —
// the only positive signal would be this test failing.
func TestOrphanRule_CacheFoldBranch_PreludeImplicitUse(t *testing.T) {
	// stringkit declares Pad — a foreign-to-todo type.
	stringkitPad := `pub struct Pad {
  width: Int
}
`
	// todo's main.nomi uses Display purely via the prelude — no
	// explicit `import std/display.{Display}`. This is the key
	// difference from TestOrphanImpl_CrossModuleRejected.
	todoMain := `import {
  stringkit/pad.{Pad}
}

impl Display for Pad {
  fn to_string(_p: Pad): String { "pad" }
}

fn main() {
  Unit
}
`
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, stringkitPad, nil)

	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	// nil, nil for primitives + modules: forces stdlib resolution
	// through the slow resolveImport path that writes to cache, which
	// is the exact path the cache-side fold of filesByKey covers.
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, todoRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	var orphanErr *analysis.TypeError
	for i := range fa.TypeErrors {
		e := &fa.TypeErrors[i]
		if strings.Contains(e.Message, "orphan impl") {
			orphanErr = e
			break
		}
	}
	if orphanErr == nil {
		// Dump all errors to make debugging regressions easier
		// (e.g. stdlib loading misconfigured).
		var allMsgs []string
		for _, e := range fa.TypeErrors {
			allMsgs = append(allMsgs, e.Message)
		}
		t.Fatalf("expected an `orphan impl` TypeError from prelude-implicit Display use, got %d errors: %v",
			len(fa.TypeErrors), allMsgs)
	}
	for _, substr := range []string{"Display", "Pad", "todo"} {
		if !strings.Contains(orphanErr.Message, substr) {
			t.Errorf("orphan error missing %q: %s", substr, orphanErr.Message)
		}
	}
}
func TestBuildProject_NestedStdlibKeyVisible(t *testing.T) {
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(
		"import std/_fixtures/nested/deeper/module.Fixture\nfn main() { Unit }\n",
	))
	fixtureNodes, _ := parser.ParseWithRecovery(lexer.Lex("pub type Fixture\n"))
	baseLoader := std.MakeLoader()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		if strings.Join(modulePath, "/") == "_fixtures/nested/deeper/module" {
			return fixtureNodes, nil
		}
		return baseLoader(projectRoot, modulePath)
	}

	fa, cache, _ := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, "", loader,
	)
	if _, ok := cache["std/_fixtures/nested/deeper/module"]; !ok {
		t.Fatalf("project cache omitted deep nested stdlib key; keys=%v", cache)
	}
	if errs := analysis.CheckTypes(fa, entryNodes); len(errs) != 0 {
		t.Fatalf("nested stdlib import produced type errors: %v", errs)
	}
}
