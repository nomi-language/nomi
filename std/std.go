package std

import (
	"embed"
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// The stdlib is ONE Nomi module: every public module is a flat `<name>.nomi`
// here, which `*.nomi` covers, plus the one `nomi.toml` that declares it.
// `all:_fixtures` covers the one nested tree, which is test-only source under
// a `_`-prefixed directory that `embed` otherwise skips.
//
// It used to be `all:*`, because four adapter directories — `calendar/`,
// `http/`, `random/`, `regex/` — held Go support and their DIRECTORY ENTRIES
// were what FirstPartyModulePaths read. Those packages are siblings now
// (`nomi/stdcalendar` and friends) and `std/` holds Nomi source and this file,
// so the pattern names what it wants instead of everything.
//
//go:embed *.nomi nomi.toml all:_fixtures
var stdlibFS embed.FS

// loadMu serializes Load.
//
// The process-wide writes to analysis.TypeBool, TypeOrdering and
// TypeAssertionFailure happen inside BuildProjectWithCache
// (analysis.installCanonicalStdTypes), under sync.Once. A sync.Once orders its
// function against other Do CALLERS only. Another goroutine's Load reads
// TypeBool in analysis.NewTypeRegistry without calling Do, so two concurrent
// Loads race. Measured under the harness in
// internal/irbuild/concurrentemit_test.go when the write lived here in std:
//
//	WRITE  std.stdDeriveSingletons.func1  std.go:404  (analysis.TypeBool)
//	READ   analysis.NewTypeRegistry       type_registry.go:15
//
// WHY A MUTEX HERE RATHER THAN ACCESSORS IN analysis. The three variables have
// about forty read sites across analysis/checker.go, type_registry.go,
// type_builder.go, internal/hoverdoc and internal/irbuild; turning each into an
// atomic accessor is the textbook fix and would be a forty-site refactor of a
// package this change has no other business in. It is also aimed at the wrong
// thing: the write happens in exactly ONE place, once per process, and the
// racing read happens inside the same call to Load. Making Load a critical
// section puts both accesses under one lock, and a reader outside Load is
// ordered by its own Load — every analysis pass in the repository gets its
// *StdLib from one.
//
// PRICED, not assumed: Load is 38ms and is not memoized (measured, four
// consecutive loads: 39.6, 37.9, 38.2, 39.3ms). Serializing it costs at most
// 38ms of overlap per concurrent caller, against the ~3.3s a single stdlib
// lowering took when measured and the 1006s this repository's slowest package spends.
//
// Load itself still builds a fresh analysis per call; the memoized one is
// Shared, which the front end and internal/irbuild read.
var loadMu sync.Mutex

// loadCount is how many times Load has run in this process.
var loadCount atomic.Int64

// LoadCount reports how many times Load has run in this process.
//
// Load is a full stdlib parse and analysis, about 57ms, and a caller that asks
// it a fixed question per module or per signature multiplies that: on
// 2026-09-26 one `nomi run hello.nomi` under the VM ran it 169 times and spent
// most of its 10s there. Tests read this to hold the count to a small constant
// (internal/irbuild/stdloadcount_test.go).
func LoadCount() int64 { return loadCount.Load() }

// StdLib holds the parsed stdlib data — the prelude scope and per-module scopes.
type StdLib struct {
	// Primitives is the prelude module scope (loaded from stdlib/prelude.nomi).
	// Historical naming — used as the parent scope for every user file. Stdlib
	// modules themselves do NOT receive it; they import their dependencies
	// explicitly.
	Primitives *analysis.Scope
	Modules    map[string]*analysis.Scope
	Files      map[string]*analysis.FileAnalysis
	// Nodes is each module's post-synthesis, analyzed AST — the exact node
	// slice CheckTypes ran over, keyed the same way as Files. Retained rather
	// than discarded because a backend that lowers stdlib source has to lower
	// the tree the analyzer checked: re-parsing the embedded .nomi would run a
	// second, drifting front end, and the two would disagree silently the
	// first time a synthesis pass changed.
	Nodes map[string][]ast.Node
	// Errors holds every diagnostic analyzing the stdlib produced, keyed by
	// module name: the build-phase errors in each FileAnalysis and the
	// CheckTypes errors. A stdlib that analyzes cleanly leaves it empty, and
	// TestStdlib_AnalyzesWithoutErrors holds it there. Nothing reports these
	// to users, so a stdlib mistake that changes what a name means (an enum
	// variant shadowing an import, say) is visible only here.
	Errors  map[string][]analysis.TypeError
	diskDir string
}

// stdlibRoot is analysis.StdlibPath, asked once per process: it reads the
// environment, resolves the executable and stats candidate directories, and
// its answer does not change while the process runs.
var stdlibRoot = sync.OnceValues(analysis.StdlibPath)

// SourcePath is the path a module's source is named by in positions and
// messages: the file in the bundled source tree when there is one, and
// otherwise where FileURI would materialize it. Unlike FileURI it touches no
// file: no per-module stat and no materialization, which only an editor
// opening the file needs. The lowering names every stdlib module's positions
// by it at startup.
func (lib *StdLib) SourcePath(moduleName string) string {
	physicalPath, ok := embeddedStdlibSourcePath(moduleName)
	if !ok {
		physicalPath = moduleName + ".nomi"
	}
	if ok {
		if root, err := stdlibRoot(); err == nil {
			abs, _ := filepath.Abs(filepath.Join(root, filepath.FromSlash(physicalPath)))
			return abs
		}
	}
	dir := lib.diskDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache", "nomi", "std")
	}
	return filepath.Join(dir, filepath.FromSlash(physicalPath))
}

// FileURI returns a file:// URI for the given module's .nomi file. When the
// bundled source tree is available, it returns the real source path so editor
// navigation stays connected to the worktree. It materializes embedded source
// to ~/.cache/nomi/std/ only when the physical source tree is unavailable.
func (lib *StdLib) FileURI(moduleName string) string {
	if physicalPath, ok := embeddedStdlibSourcePath(moduleName); ok {
		if root, err := stdlibRoot(); err == nil {
			sourcePath := filepath.Join(root, filepath.FromSlash(physicalPath))
			if _, err := os.Stat(sourcePath); err == nil {
				abs, _ := filepath.Abs(sourcePath)
				return "file://" + abs
			}
		}
	}
	if lib.diskDir == "" {
		home, _ := os.UserHomeDir()
		lib.diskDir = filepath.Join(home, ".cache", "nomi", "std")
		// Clean up the former cache directory name so editor jumps do not
		// leave two materialized stdlib trees on disk.
		os.RemoveAll(filepath.Join(home, ".cache", "nomi", "stdlib"))
		// Remove and recreate to clear stale files from previous builds.
		os.RemoveAll(lib.diskDir)
		os.MkdirAll(lib.diskDir, 0o755)
		fs.WalkDir(stdlibFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isEmbeddedFile(path) {
				return nil
			}
			content, err := stdlibFS.ReadFile(path)
			if err != nil {
				return nil
			}
			diskPath := filepath.Join(lib.diskDir, filepath.FromSlash(path))
			os.MkdirAll(filepath.Dir(diskPath), 0o755)
			os.WriteFile(diskPath, content, 0o644)
			return nil
		})
	}
	physicalPath, ok := embeddedStdlibSourcePath(moduleName)
	if !ok {
		physicalPath = moduleName + ".nomi"
	}
	return "file://" + filepath.Join(lib.diskDir, filepath.FromSlash(physicalPath))
}

// ReadFile reads a stdlib .nomi file by logical module name: "maybe" maps to
// maybe.nomi, and so does an adapter facade such as "regex" — every public
// stdlib module is a flat file here.
func ReadFile(moduleName string) ([]byte, bool) {
	path, ok := embeddedStdlibSourcePath(moduleName)
	if !ok {
		return nil, false
	}
	content, err := stdlibFS.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return content, true
}

func embeddedStdlibSourcePath(moduleName string) (string, bool) {
	direct := moduleName + ".nomi"
	if !isEmbeddedStdlibSource(direct) {
		return "", false
	}
	if _, err := stdlibFS.ReadFile(direct); err != nil {
		return "", false
	}
	return direct, true
}

func isEmbeddedFile(path string) bool {
	return strings.HasSuffix(path, ".nomi")
}

func isEmbeddedStdlibSource(path string) bool {
	return isEmbeddedFile(path) && !strings.HasPrefix(path, "_fixtures/")
}

// MakeLoader returns the analysis.FileLoader the front end loads with.
// Stdlib-routed loads use embedded
// stdlib files; ordinary project loads read disk first so a local file
// such as json.nomi is not shadowed by std/json.nomi. Centralised here
// so both call sites share one implementation — runtime.makeLoader
func MakeLoader() analysis.FileLoader { return MakeReportingLoader(nil) }

// MakeReportingLoader is MakeLoader that hands the syntax errors of every
// project file it reads to report, with the file's path. A file with syntax
// errors still loads, as far as recovery reads it. Nil reports nothing.
func MakeReportingLoader(report func(path string, errs []parser.ParseError)) analysis.FileLoader {
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		stdlibLoad := isStdlibLoad(projectRoot, modulePath)
		stdlibName := embeddedStdlibName(modulePath)
		if stdlibLoad {
			if _, ok := embeddedStdlibSourcePath(stdlibName); !ok {
				return nil, fmt.Errorf("module not found: %s", filepath.Join(modulePath...))
			}
		}
		if stdlibLoad {
			if data, ok := ReadFile(stdlibName); ok {
				return parseNomi(data), nil
			}
		}
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		if data, err := os.ReadFile(filePath); err == nil {
			nodes, errs := parser.ParseWithRecovery(lexer.Lex(string(data)))
			if report != nil && !stdlibLoad {
				report(filePath, errs)
			}
			return nodes, nil
		}
		// The filesystem read failed for a stdlib-routed load — either there is
		// no filesystem (GOOS=js wasm) or the materialized stdlib path is absent.
		// Fall back to the embedded stdlib so stdlib modules resolve straight
		// from the binary.
		if stdlibLoad {
			if data, ok := ReadFile(stdlibName); ok {
				return parseNomi(data), nil
			}
		}
		return nil, fmt.Errorf("module not found: %s", filepath.Join(modulePath...))
	}
}

func parseNomi(data []byte) []ast.Node {
	tokens := lexer.Lex(string(data))
	nodes, _ := parser.ParseWithRecovery(tokens)
	return nodes
}

func embeddedStdlibName(modulePath []string) string {
	if len(modulePath) > 1 && modulePath[0] == "std" {
		return strings.Join(modulePath[1:], "/")
	}
	return strings.Join(modulePath, "/")
}

// embeddedStdlibLogicalName is analysis.StdlibLogicalModuleName over an
// embedded path. Delegated rather than repeated: runtime derived the same
// collapse independently and got it wrong, which loaded std/calendar twice.
func embeddedStdlibLogicalName(path string) string {
	return analysis.StdlibLogicalModuleName(path)
}

// embeddedStdlibNames returns every logical stdlib source path, preserving
// canonical dependency order while collapsing a co-located root facade such
// as regex/regex.nomi to the public module name regex.
func embeddedStdlibNames() []string {
	embedded := make(map[string]bool)
	var discovered []string
	_ = fs.WalkDir(stdlibFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isEmbeddedStdlibSource(path) {
			return err
		}
		name := embeddedStdlibLogicalName(path)
		if name != "prelude" {
			embedded[name] = true
			discovered = append(discovered, name)
		}
		return nil
	})

	names := make([]string, 0, len(embedded))
	seen := make(map[string]bool, len(embedded))
	for _, name := range analysis.StdlibLoadOrder {
		if embedded[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	for _, name := range discovered {
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names
}

func isStdlibLoad(projectRoot string, modulePath []string) bool {
	if len(modulePath) > 0 && modulePath[0] == "std" {
		return true
	}
	if projectRoot == "" {
		return true
	}
	// stdlibRoot, not analysis.StdlibPath: this runs on every module load (144
	// times for hello world, 95 of them inside std.Load), and an uncached
	// StdlibPath resolves the executable, walks its symlinks (an lstat per path
	// component) and stats candidate directories, about 16 us each time.
	stdRoot, err := stdlibRoot()
	if err != nil {
		return false
	}
	return filepath.Clean(projectRoot) == filepath.Clean(stdRoot)
}

func stdlibDependencyImport(name string, line int) *ast.ImportStmt {
	parts := strings.Split(name, "/")
	nodes := make([]ast.Node, 0, len(parts)+1)
	nodes = append(nodes, &ast.Ident{Name: "std", Line: line, Col: 8})
	col := 12
	for _, part := range parts {
		nodes = append(nodes, &ast.Ident{Name: part, Line: line, Col: col})
		col += len(part) + 1
	}
	return &ast.ImportStmt{
		ModulePath: nodes,
		Line:       line,
		Col:        1,
	}
}

// Shared is the process's one analysis of the stdlib: the first call runs
// Load and every later call answers the same *StdLib. The front end
// (internal/frontend) resolves a program against it and internal/irbuild lowers
// the stdlib from it, so `nomi run` analyzes the stdlib once rather than once
// per consumer. It is what the LSP has always done with its one Load.
//
// Callers only read it. A project build writes nothing into a shared
// FileAnalysis it does not own (analysis.buildProjectWithCache rebinds
// ProjectImpls only on files that build owns, or on one that has none).
var Shared = sync.OnceValue(Load)

// Load parses all embedded stdlib .nomi files and returns the analyzed
// result. Internally delegates to BuildProjectWithCache so every stdlib
// FA flows through the same Sweep A/B/C two-pass user code uses,
// including cyclic-import support (file A imports B, file B imports A).
//
// Mechanism: synthesize a throwaway entry that imports every stdlib
// module so DiscoverProject walks them all into proj.Files, then let
// BuildProjectWithCache orchestrate the sweeps. The entry FA is
// discarded; we repackage the per-stdlib cache into *StdLib.
//
// SERIALIZED for the whole body: both the write to analysis.TypeBool and the
// racing read of it are inside BuildProjectWithCache. See loadMu.
func Load() *StdLib {
	loadMu.Lock()
	defer loadMu.Unlock()
	loadCount.Add(1)

	lib := &StdLib{
		Modules: make(map[string]*analysis.Scope),
		Files:   make(map[string]*analysis.FileAnalysis),
		Nodes:   make(map[string][]ast.Node),
		Errors:  make(map[string][]analysis.TypeError),
	}

	// Synthesize an internal entry whose imports cover every stdlib module,
	// making DiscoverProject walk them all. Bare module imports are no longer
	// a surface syntax, but the bootstrapper still needs a dependency-only
	// edge that doesn't bind an API name in user code.
	var entryNodes []ast.Node
	line := 1
	for _, name := range embeddedStdlibNames() {
		entryNodes = append(entryNodes, stdlibDependencyImport(name, line))
		line++
	}
	entryNodes = append(entryNodes, stdlibDependencyImport("prelude", line))

	loader := MakeLoader()
	// projectRoot = "" so DiscoverProject skips manifest + go.mod loading
	// for the synthetic entry. This matters because stdlib's own nomi.toml
	// declares `name = "std"`, which the manifest validator (rightly)
	// rejects when loaded as a user project — without this, the synthetic
	// entry would fail discovery with "[module] name 'std' is reserved".
	// Stdlib lookups still resolve via the always-on stdlib injection in
	// discovery.go (ModuleIndex["std"] = StdlibPath()), and file content
	// comes from MakeLoader which reads embed first.
	_, cache, extendedNodes := analysis.BuildProjectWithCache(
		entryNodes,
		nil, // primitives: we're BUILDING the prelude scope right now
		nil, // modules:    we're BUILDING the per-module scopes right now
		nil, // stdlibFAs:  we're BUILDING the stdlib FAs right now
		"",
		loader,
	)

	// analysis.TypeBool, TypeOrdering and TypeAssertionFailure are installed by
	// BuildProjectWithCache above, between its type-shell sweep and its
	// BuildTypes sweep (analysis.installCanonicalStdTypes). They used to be
	// installed HERE, after BuildProjectWithCache returned, which made the first
	// load in a process different from every later one: BuildTypes resolves
	// every std file's `Bool` through NewTypeRegistry, which reads TypeBool, so
	// the first load's std signatures carried the hand-written fallback Bool
	// from analysis/types.go (no Origin, no `embeds` payloads). internal/irbuild
	// caches one stdlib lowering per process, so a process that lowered std
	// before analyzing any program kept that state: std/json lost
	// `impl FromJson for Bool`, and a derived FromJson for a struct with a Bool
	// field was refused. Moving the install only as far as "before CheckTypes"
	// did not fix it, because BuildTypes had already run.
	// TestStdOrder_LoweringStdFirstMatchesAnalyzingFirst guards it.
	names := make([]string, 0, len(cache))
	for key := range cache {
		if !strings.HasPrefix(key, "std/") {
			continue
		}
		name := strings.TrimPrefix(key, "std/")
		names = append(names, name)
		lib.Files[name] = cache[key]
		lib.Modules[name] = cache[key].ModuleScope
		lib.Nodes[name] = extendedNodes[key]
	}
	// Sorted so the checking order is a function of the stdlib and not of map
	// iteration. The previous loop's order was already the map's, and two fresh
	// loads were measured byte-identical through it, so this is not a fix for an
	// observed fault — it is one fewer place a later fault could hide, in a
	// function whose whole defect above was an ordering one.
	sort.Strings(names)

	// CheckTypes per stdlib FA. BuildProjectWithCache stops after Sweep
	// C-types; CheckTypes adds impl-body conformance recordings to
	// fa.ImplManifest, which the runtime impl-bridge consumes. Diagnostics
	// are kept in lib.Errors rather than reported: the user-facing project
	// pipeline reports errors against user code, and a stdlib error is a
	// defect in this repository, caught by TestStdlib_AnalyzesWithoutErrors.
	for _, name := range names {
		errs := append([]analysis.TypeError(nil), lib.Files[name].TypeErrors...)
		errs = append(errs, analysis.CheckTypes(lib.Files[name], lib.Nodes[name])...)
		if len(errs) > 0 {
			lib.Errors[name] = errs
		}
	}

	if preludeFA, ok := lib.Files["prelude"]; ok {
		lib.Primitives = preludeFA.ModuleScope
	} else {
		lib.Primitives = analysis.NewScope(nil)
	}

	// Every later build parents user scopes under these. Marked only now,
	// so the stdlib's own lexical scopes are recorded while it is analyzed.
	lib.Primitives.MarkShared()
	for _, scope := range lib.Modules {
		scope.MarkShared()
	}
	for _, fa := range lib.Files {
		if fa.ModuleScope != nil {
			fa.ModuleScope.MarkShared()
		}
	}

	return lib
}
