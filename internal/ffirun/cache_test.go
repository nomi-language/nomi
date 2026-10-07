package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stageMinimalProject writes the smallest possible Nomi project to a
// tempdir: a go.mod referencing one fake required module (the
// discovery + Discover details don't matter for cache tests; we
// supply discovered list directly to ensureWrapper).
//
// Returns the project root absolute path. Tests pass it to
// ensureWrapper alongside a hand-crafted discovered slice.
func stageMinimalProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	goMod := "module cachetest\n\ngo 1.26.3\n\nrequire example.com/binding v0.0.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return root
}

func makeDiscovered() []DiscoveredPackage {
	return []DiscoveredPackage{
		{ImportPath: "example.com/binding", Alias: "binding"},
	}
}

// TestCache_FirstRunGeneratesAll checks the cold-start path: an
// empty cache directory ends up with main.go, go.mod,
// discovered.json, and hash.json — every file ensureWrapper claims
// to write.
func TestCache_FirstRunGeneratesAll(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	if err := ensureWrapper(cacheDir, projectRoot, makeDiscovered(), []string{"example.com/binding"}); err != nil {
		t.Fatalf("ensureWrapper: %v", err)
	}
	for _, name := range []string{"main.go", "go.mod", "hash.json", "discovered.json"} {
		path := filepath.Join(cacheDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}

// TestCache_NomiSourceEditNoRegen is the heart of the
// "edit-run loop stays fast" guarantee: a .nomi-only edit does NOT
// shift any of the four hash inputs (go.mod, go.sum, template,
// discovered), so ensureWrapper short-circuits and main.go's mtime
// stays fixed. If this test fails it usually means the hash record
// is accidentally picking up .nomi state — investigate
// computeHashes' inputs.
func TestCache_NomiSourceEditNoRegen(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := makeDiscovered()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}
	mainPath := filepath.Join(cacheDir, "main.go")
	info0, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after first run: %v", err)
	}
	// Touch a .nomi file in the project — irrelevant to hash inputs.
	if err := os.WriteFile(
		filepath.Join(projectRoot, "scratch.nomi"),
		[]byte("// scratch\nfn x(): Int { 1 }\n"),
		0o644,
	); err != nil {
		t.Fatalf("write scratch.nomi: %v", err)
	}
	// Sleep past mtime resolution so any rewrite would be detectable.
	time.Sleep(20 * time.Millisecond)
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("second ensureWrapper: %v", err)
	}
	info1, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after second run: %v", err)
	}
	if !info0.ModTime().Equal(info1.ModTime()) {
		t.Errorf("expected main.go mtime preserved across .nomi-only edit; "+
			"before=%v after=%v", info0.ModTime(), info1.ModTime())
	}
}

// TestCache_GoModEditRegens is the inverse: any change to go.mod
// shifts the GoMod hash; ensureWrapper sees the mismatch and
// regenerates everything. mtime advances.
func TestCache_GoModEditRegens(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := makeDiscovered()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}
	mainPath := filepath.Join(cacheDir, "main.go")
	info0, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after first run: %v", err)
	}
	goModPath := filepath.Join(projectRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	// Append a benign trailing comment so the file content shifts but
	// the module is still parseable.
	if err := os.WriteFile(goModPath, append(data, []byte("\n// trailing edit\n")...), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("second ensureWrapper: %v", err)
	}
	info1, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after second run: %v", err)
	}
	if info0.ModTime().Equal(info1.ModTime()) {
		t.Errorf("expected main.go regen after go.mod edit; mtime unchanged at %v", info0.ModTime())
	}
}

func TestCache_GoModEditRemovesCachedWrapperBinary(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := makeDiscovered()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}
	binPath := cachedWrapperBinaryPathForDir(cacheDir)
	if err := os.WriteFile(binPath, []byte("stale"), 0o755); err != nil {
		t.Fatalf("write fake wrapper binary: %v", err)
	}
	goModPath := filepath.Join(projectRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if err := os.WriteFile(goModPath, append(data, []byte("\n// binary invalidation\n")...), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("second ensureWrapper: %v", err)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected cached wrapper binary to be removed after wrapper regen, stat err=%v", err)
	}
}

// TestCache_TemplateBumpRegens simulates a `nomi` binary upgrade
// that ships a new wrapper template by tampering with hash.json's
// Template field so it no longer matches the in-memory templateHash.
// ensureWrapper must see the mismatch and regenerate main.go from
// the current template.
func TestCache_TemplateBumpRegens(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := makeDiscovered()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}
	mainPath := filepath.Join(cacheDir, "main.go")
	info0, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after first run: %v", err)
	}
	// Tamper with hash.json: replace the recorded template hash with a
	// sentinel that won't match anything the running binary produces.
	hashPath := filepath.Join(cacheDir, "hash.json")
	hashBytes, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("read hash.json: %v", err)
	}
	bumped := strings.Replace(string(hashBytes), templateHash, "0000000000ff", 1)
	if bumped == string(hashBytes) {
		t.Fatalf("could not locate templateHash %q in hash.json content:\n%s", templateHash, hashBytes)
	}
	if err := os.WriteFile(hashPath, []byte(bumped), 0o644); err != nil {
		t.Fatalf("write tampered hash.json: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := ensureWrapper(cacheDir, projectRoot, discovered, []string{"example.com/binding"}); err != nil {
		t.Fatalf("second ensureWrapper: %v", err)
	}
	info1, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("stat after second run: %v", err)
	}
	if info0.ModTime().Equal(info1.ModTime()) {
		t.Errorf("expected main.go regen after template-hash mismatch; mtime unchanged at %v", info0.ModTime())
	}
}

// TestCache_DiffersAcrossProjects sanity-checks the cache-key
// scheme: two different project roots get distinct cache dirs.
func TestCache_DiffersAcrossProjects(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	a, err := cacheDirForProject("/path/to/projectA")
	if err != nil {
		t.Fatalf("projectA: %v", err)
	}
	b, err := cacheDirForProject("/path/to/projectB")
	if err != nil {
		t.Fatalf("projectB: %v", err)
	}
	if a == b {
		t.Errorf("expected distinct cache dirs; both got %s", a)
	}
}

// TestCache_AbsolutizesReplaces verifies the absolutization step the
// design-doc calls for: a relative replace in the project's go.mod
// becomes an absolute path in the cache's copy, so the wrapper can
// resolve the binding when run from the cache dir (which is
// elsewhere on disk).
func TestCache_AbsolutizesReplaces(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := t.TempDir()
	goMod := "module cachetest\n\ngo 1.26.3\n\nrequire example.com/binding v0.0.0\n\nreplace example.com/binding => ./local-binding\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	if err := ensureWrapper(cacheDir, projectRoot, makeDiscovered(), []string{"example.com/binding"}); err != nil {
		t.Fatalf("ensureWrapper: %v", err)
	}
	cacheGoMod, err := os.ReadFile(filepath.Join(cacheDir, "go.mod"))
	if err != nil {
		t.Fatalf("read cache go.mod: %v", err)
	}
	wantAbs := filepath.Join(projectRoot, "local-binding")
	if !strings.Contains(string(cacheGoMod), wantAbs) {
		t.Errorf("expected cache go.mod to contain absolute replace target %q;\ngot:\n%s",
			wantAbs, cacheGoMod)
	}
	if !strings.Contains(string(cacheGoMod), "module nomi-ffi-wrapper") {
		t.Errorf("expected cache go.mod to use wrapper module path; got:\n%s", cacheGoMod)
	}
	if !strings.Contains(string(cacheGoMod), "replace cachetest => "+projectRoot) {
		t.Errorf("expected cache go.mod to replace user module to project root; got:\n%s", cacheGoMod)
	}
	// And make sure the relative form was rewritten, not appended.
	if strings.Contains(string(cacheGoMod), "=> ./local-binding") {
		t.Errorf("relative replace target survived in cache go.mod:\n%s", cacheGoMod)
	}
}

// TestCache_StagesCompilerModuleAndGoFloor pins the directives a staged wrapper
// needs in order to build at all: a local replace of the compiler's module,
// which holds the runtime library too, and the compiler's `go` floor.
//
// Toolchain selection reads only the main module's `go` line, so a wrapper
// stamped older than the compiler module fails with "requires go >= ...
// (running go ...)" whatever GOTOOLCHAIN says. The project go.mod below
// deliberately declares an older `go` than the compiler's to exercise that.
func TestCache_StagesCompilerModuleAndGoFloor(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := t.TempDir()
	goMod := "module cachetest\n\ngo 1.22\n\nrequire github.com/nomi-language/nomi v0.0.0\n\nreplace github.com/nomi-language/nomi => " + repoRoot(t) + "\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	if err := ensureWrapper(cacheDir, projectRoot, makeDiscovered(), []string{"nomi"}); err != nil {
		t.Fatalf("ensureWrapper: %v", err)
	}
	staged, err := os.ReadFile(filepath.Join(cacheDir, "go.mod"))
	if err != nil {
		t.Fatalf("read cache go.mod: %v", err)
	}
	got := string(staged)

	nomiRoot, err := nomiModuleRoot()
	if err != nil {
		t.Fatalf("nomiModuleRoot: %v", err)
	}
	if want := "replace " + compilerModulePath + " => " + nomiRoot; !strings.Contains(got, want) {
		t.Errorf("staged go.mod is missing %q; a wrapper without it tries to "+
			"download an unpublished module.\ngot:\n%s", want, got)
	}
	if strings.Contains(got, compilerModulePath+"/rt") {
		t.Errorf("staged go.mod names the runtime library as its own module; it is "+
			"a package of %s.\ngot:\n%s", compilerModulePath, got)
	}

	compilerGo, err := compilerGoDirective()
	if err != nil {
		t.Fatalf("compilerGoDirective: %v", err)
	}
	if !strings.Contains(got, "go "+compilerGo+"\n") {
		t.Errorf("staged go.mod does not declare go %s; the project asked for "+
			"1.22 and the wrapper links a module that requires %s, so the floor "+
			"has to be raised.\ngot:\n%s", compilerGo, compilerGo, got)
	}
}

// TestCache_RoundTripHash sanity-checks that computeHashes + write/
// read are reflexive: the bytes we write decode back to exactly the
// record we wrote. Catches regressions in the JSON field tags.
func TestCache_RoundTripHash(t *testing.T) {
	dir := t.TempDir()
	want := hashRecord{
		GoMod:      "sha256:" + strings.Repeat("a", 64),
		GoSum:      "sha256:" + strings.Repeat("b", 64),
		Template:   "sha256:" + templateHash,
		Discovered: "sha256:" + strings.Repeat("c", 64),
		Compiler:   "sha256:" + strings.Repeat("d", 64),
	}
	if err := writeHashRecord(dir, want); err != nil {
		t.Fatalf("writeHashRecord: %v", err)
	}
	got, ok := readHashRecord(dir)
	if !ok {
		t.Fatal("readHashRecord: expected ok=true")
	}
	if got != want {
		t.Errorf("round-trip mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestCache_WarmWrapperBinaryReused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go build; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "go.mod"), "module testproject\n\ngo 1.26.3\n\nrequire example.com/binding/source v0.0.0\n\nrequire github.com/nomi-language/nomi v0.0.0\n\nreplace example.com/binding/source => ./binding\n\nreplace github.com/nomi-language/nomi => "+repoRoot(t)+"\n")
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn echo_upper(s: String): String go binding.EchoUpper
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	bin, err := ensureCachedWrapperBinary(res)
	if err != nil {
		t.Fatalf("first ensureCachedWrapperBinary: %v", err)
	}
	info0, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat wrapper binary: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	binAgain, err := ensureCachedWrapperBinary(res)
	if err != nil {
		t.Fatalf("second ensureCachedWrapperBinary: %v", err)
	}
	if binAgain != bin {
		t.Fatalf("expected same binary path, got %q then %q", bin, binAgain)
	}
	info1, err := os.Stat(binAgain)
	if err != nil {
		t.Fatalf("stat wrapper binary after second ensure: %v", err)
	}
	if !info0.ModTime().Equal(info1.ModTime()) {
		t.Fatalf("expected warm wrapper binary mtime to be preserved; before=%v after=%v", info0.ModTime(), info1.ModTime())
	}
}

func TestCache_ConcurrentWrapperBinaryBuildsDoNotShareTempPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go build; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "go.mod"), "module testproject\n\ngo 1.26.3\n\nrequire example.com/binding/source v0.0.0\n\nrequire github.com/nomi-language/nomi v0.0.0\n\nreplace example.com/binding/source => ./binding\n\nreplace github.com/nomi-language/nomi => "+repoRoot(t)+"\n")
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn echo_upper(s: String): String go binding.EchoUpper
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.Remove(cachedWrapperBinaryPath(res)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove warm binary: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bin, err := ensureCachedWrapperBinary(res)
			if err != nil {
				errs <- err
				return
			}
			if bin != cachedWrapperBinaryPath(res) {
				errs <- os.ErrInvalid
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ensureCachedWrapperBinary: %v", err)
		}
	}
	if _, err := os.Stat(cachedWrapperBinaryPath(res)); err != nil {
		t.Fatalf("stat wrapper binary: %v", err)
	}
}

// The Discovered field must move when anything the wrapper compiles in moves.
// The declaring file is one: the wrapper registers a module-qualified binding
// under its bare name only when the entry it runs is the file that declared it.
// Keyed on names alone, a script run as hi at the go.mod root and then as
// bin/hi reused the first wrapper, which still named the root file, and bin/hi
// crossed into Go under a key nothing answered. A source position is not
// compiled into behaviour and must not move it, or every edit above a binding
// would rebuild the wrapper.
func TestCache_DiscoveredHashCoversWhatTheWrapperCompilesIn(t *testing.T) {
	projectRoot := stageMinimalProject(t)
	base := func() []DiscoveredPackage {
		return []DiscoveredPackage{{
			ImportPath: "example.com/binding",
			Alias:      "binding",
			Types: []DiscoveredType{{
				Key: "hi.Box", EntryKey: "Box", TypeName: "Box",
				Declaration: "host type Box", SourceFile: "/p/hi", SourceLine: 3,
			}},
			Exports: []DiscoveredExport{{
				Key: "hi.upper", EntryKey: "upper", FuncName: "Upper",
				Declaration: "host fn upper(s: String): String",
				SourceFile:  "/p/hi", SourceLine: 5,
			}},
		}}
	}
	hash := func(d []DiscoveredPackage) string {
		t.Helper()
		r, err := computeHashes(projectRoot, d, nil)
		if err != nil {
			t.Fatalf("computeHashes: %v", err)
		}
		return r.Discovered
	}
	want := hash(base())

	moves := map[string]func(d []DiscoveredPackage){
		"export source file":     func(d []DiscoveredPackage) { d[0].Exports[0].SourceFile = "/p/bin/hi" },
		"export entry key":       func(d []DiscoveredPackage) { d[0].Exports[0].EntryKey = "" },
		"export also declared":   func(d []DiscoveredPackage) { d[0].Exports[0].AlsoDeclaredIn = []string{"/p/bin/hi"} },
		"export declaration":     func(d []DiscoveredPackage) { d[0].Exports[0].Declaration = "host fn upper(s: String): Int" },
		"export inline Go body":  func(d []DiscoveredPackage) { d[0].Exports[0].GoBody = "return s" },
		"export inline params":   func(d []DiscoveredPackage) { d[0].Exports[0].ParamDecls = "s string" },
		"export inline result":   func(d []DiscoveredPackage) { d[0].Exports[0].ReturnDecl = "string" },
		"type source file":       func(d []DiscoveredPackage) { d[0].Types[0].SourceFile = "/p/bin/hi" },
		"type entry key":         func(d []DiscoveredPackage) { d[0].Types[0].EntryKey = "" },
		"type Go expression":     func(d []DiscoveredPackage) { d[0].Types[0].GoTypeExpr = "*binding.Box" },
		"package Go declaration": func(d []DiscoveredPackage) { d[0].GoDecls = []string{"type T int"} },
	}
	for name, move := range moves {
		d := base()
		move(d)
		if hash(d) == want {
			t.Errorf("%s changed and the Discovered hash did not", name)
		}
	}

	d := base()
	d[0].Exports[0].SourceLine, d[0].Exports[0].SourceCol = 40, 9
	d[0].Types[0].SourceLine = 30
	if hash(d) != want {
		t.Errorf("moving a binding's line changed the Discovered hash")
	}
}

// A struct a binding converts is compiled into its adapter field by field,
// and the binding's declaration names the struct only. So an edit to the
// struct, or to a struct nested in it, must move the hash: keyed on the
// declaration alone, a renamed field of a nested struct kept the cached
// adapter building the old struct, and the program failed reading the new
// field at run time where a fresh wrapper refuses the binding at load. An
// edit that changes no converted shape must not move it, or every edit to a
// file beside a binding would rebuild the wrapper.
func TestCache_HashCoversTheShapesABindingConverts(t *testing.T) {
	projectRoot := stageMinimalProject(t)
	shapesPath := filepath.Join(projectRoot, "shapes.nomi")
	mainPath := filepath.Join(projectRoot, "main.nomi")
	const shapes = `gopkg "example.com/binding"

pub struct Inner {
    count: Int
    label: String
}

pub struct Outer {
    name: String
    inner: Inner
    timeout: Duration
}

pub struct Unrelated {
    x: Int
}

pub fn make(): Outer go binding.Make

pub fn twice(n: Int): Int {
    n * 2
}
`
	const main = `import {
    std/io
    shapes
}

fn main() {
    io.print(shapes.make().name)
}
`
	hash := func(shapesSrc, mainSrc string) hashRecord {
		t.Helper()
		if err := os.WriteFile(shapesPath, []byte(shapesSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mainPath, []byte(mainSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		discovered, err := Discover(projectRoot)
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(discovered) != 1 || len(discovered[0].Exports) != 1 {
			t.Fatalf("Discover found %+v, want the one binding", discovered)
		}
		r, err := computeHashes(projectRoot, discovered, nil)
		if err != nil {
			t.Fatalf("computeHashes: %v", err)
		}
		return r
	}
	want := hash(shapes, main)

	moves := map[string]string{
		"nested field renamed":          strings.Replace(shapes, "    label: String", "    title: String", 1),
		"nested field retyped":          strings.Replace(shapes, "    count: Int", "    count: Float", 1),
		"nested fields reordered":       strings.Replace(shapes, "    count: Int\n    label: String", "    label: String\n    count: Int", 1),
		"result field added":            strings.Replace(shapes, "    name: String\n", "    name: String\n    port: Int\n", 1),
		"std-typed field renamed":       strings.Replace(shapes, "    timeout: Duration", "    deadline: Duration", 1),
		"nested struct made a distinct": strings.Replace(shapes, "pub struct Inner {\n    count: Int\n    label: String\n}", "pub type Inner(String)", 1),
	}
	for name, src := range moves {
		if src == shapes {
			t.Fatalf("%s: the edit did not apply", name)
		}
		if hash(src, main) == want {
			t.Errorf("%s: the struct a binding converts changed and the hash did not", name)
		}
	}

	stays := map[string][2]string{
		"unrelated struct edited": {strings.Replace(shapes, "    x: Int", "    x: String", 1), main},
		"function body edited":    {strings.Replace(shapes, "n * 2", "n * 3", 1), main},
		"entry edited":            {shapes, strings.Replace(main, ".name)", ".inner.label)", 1)},
	}
	for name, src := range stays {
		if got := hash(src[0], src[1]); got != want {
			t.Errorf("%s: no converted shape changed and the hash did (%+v, want %+v)", name, got, want)
		}
	}
}
