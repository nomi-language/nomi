package analysis

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Project is the result of a discovery walk: every user-module file
// reachable from the entry, parsed into nodes. Discovery does no
// building — it just collects what's there for the project orchestrator
// (BuildProject) to feed through Sweeps A/B/C.
type Project struct {
	Entry       []ast.Node            // nodes of the entry file
	EntryPath   string                // absolute path of the entry file (may be empty)
	Files       map[string][]ast.Node // key = strings.Join(modulePath, "/")
	FilePaths   map[string]string     // same key; value = absolute file path
	ProjectRoot string
	// Manifest is the parsed nomi.toml at ProjectRoot, or nil when no
	// manifest is present. Single-file `nomi run` mode and ad-hoc test
	// fixtures that don't include a manifest stay valid — downstream
	// checks that consume the manifest (entry enforcement, cross-module
	// resolution) skip themselves when Manifest is nil.
	Manifest *Manifest
	// ModuleIndex is the modules the program's files import by name: the
	// standard library and each dependency the module graph holds
	// (BuildModuleIndex), with the names each module's files import
	// others by. Consumed by the builder's import resolver
	// (resolveImport) and by this file's discovery walk to follow
	// imports into other modules.
	ModuleIndex ModuleIndex
	// reused holds the keys of the files the walk reached whose analysis
	// the build was handed (reusedFile): it neither loaded nor parsed them,
	// and walked their imports from the nodes it was handed.
	reused map[string]bool
	// imports holds the import paths of each file the walk loaded, as
	// importsOf found them in the nodes the loader answered, by key.
	imports map[string][][]string
}

// DiscoverProjectWithManifest BFS-walks the import graph starting from
// entryNodes. Each reachable user-module file is parsed once via the loader
// and stored in p.Files keyed by its module-path string. Cycles in the
// import graph are handled naturally by the visited set.
//
// Stdlib imports (paths starting with "std") are walked like any
// other cross-module import; the bundled stdlib's root is virtually
// injected into ModuleIndex below so classify() routes `std/foo`
// through the same path as any sibling-module dep.
//
// Loader errors for individual files are tolerated (the builder
// reports the import as "no module `x`" at its path); only truly fatal
// errors return non-nil error. With a nil loader this returns an
// empty project.
//
// mfst is a pre-parsed Manifest, for callers that staged the project in
// memory (no nomi.toml on disk to read) — tour playground, doctest harness,
// embedders. When mfst is nil and projectRoot is set, the manifest is loaded
// from disk.
func DiscoverProjectWithManifest(entryNodes []ast.Node, projectRoot string, loader FileLoader, mfst *Manifest) (*Project, error) {
	return discoverProject(entryNodes, projectRoot, loader, mfst, nil, nil, nil)
}

// discoverProject is DiscoverProjectWithManifest that neither loads nor walks
// a file whose key analyzed reports: one the caller already holds an
// analysis of. A build against the shared stdlib passes the stdlib modules it
// was handed, so `import std::io` resolves to that analysis
// (resolveStdlibImport) instead of parsing and analyzing std/io and every
// module it imports again for each program. Nil walks every file.
//
// reused are the project files whose analysis the caller holds from an
// earlier build (reusedFile): a reached one is noted in Project.reused, not
// loaded, and its imports are walked from the nodes it was built from.
//
// roots are module keys of projectRoot the walk starts from besides the
// entry's imports (buildInputs.roots).
func discoverProject(entryNodes []ast.Node, projectRoot string, loader FileLoader, mfst *Manifest, analyzed func(key string) bool, reused map[string]reusedFile, roots []string) (*Project, error) {
	return discoverProjectStd(entryNodes, projectRoot, loader, mfst, analyzed, reused, roots, nil)
}

// discoverProjectStd is discoverProject that hands stdBlock the key
// (`std/<name>`) of each stdlib module a walked file imports in a block,
// when stdBlock is not nil: a module the walk leaves to the shared stdlib
// analysis, which a lazily built stdlib must have built (StdSource).
// stdJSONKey is std/json's module key.
const stdJSONKey = "std/json"

func discoverProjectStd(entryNodes []ast.Node, projectRoot string, loader FileLoader, mfst *Manifest, analyzed func(key string) bool, reused map[string]reusedFile, roots []string, stdBlock func(key string)) (*Project, error) {
	p := &Project{
		Entry:       entryNodes,
		Files:       make(map[string][]ast.Node),
		FilePaths:   make(map[string]string),
		ProjectRoot: projectRoot,
		ModuleIndex: newModuleIndex(),
	}
	// Caller-supplied manifest wins (in-memory virtual mode). Otherwise
	// load nomi.toml once per build from disk. Missing manifest is fine —
	// single-file `nomi run` and many test fixtures have no manifest, and
	// downstream consumers skip when Manifest is nil. A malformed manifest
	// is returned to the caller beside the project, which BuildProjectWithCache
	// reports once as a TypeError on the entry FA; the walk still runs, as
	// for a project with no manifest, so the program's imports, std's
	// among them, resolve as they always do and report nothing of their own.
	var manifestErr error
	if mfst != nil {
		p.Manifest = mfst
	} else if projectRoot != "" {
		if m, err := LoadManifest(projectRoot); err == nil {
			p.Manifest = m
		} else if !errors.Is(err, ErrManifestMissing) {
			manifestErr = err
		}
	}
	// The module index comes from the module graph (BuildModuleIndex).
	// No nomi.toml is an empty index, which single-file mode keeps, and so
	// is a manifest the graph cannot read, whose error is returned as a
	// malformed manifest's is, unless that manifest's own error came first.
	if projectRoot != "" {
		if idx, err := BuildModuleIndex(projectRoot); err == nil {
			p.ModuleIndex = idx
		} else if manifestErr == nil {
			manifestErr = err
		}
	}
	// The standard library is a module of every program under the key
	// name "std", which no nomi.toml lists: `import std::strings`
	// resolves through the same index as an import of a dependency. With
	// no std/ directory reachable, single-file `nomi parse` flows that
	// never touch it still work; a build that needs it fails where
	// resolveImport finds nothing for a `std/...` path.
	if stdPath, err := StdlibPath(); err == nil {
		p.ModuleIndex.Roots["std"] = stdPath
	}
	if loader == nil && stdBlock == nil {
		return p, manifestErr
	}

	currentModuleName := ""
	if p.Manifest != nil {
		currentModuleName = p.Manifest.Name
	}

	// walkItem is a file to load: the module it is in (by key name, ""
	// for the program's own), that module's root, the path within it, and
	// the key the builder's resolveImport computes for it too.
	type walkItem struct {
		module  string
		root    string
		modPath []string
		key     string
	}

	// classify is the file an import path written in a file of module in
	// names (ModuleIndex.target).
	classify := func(in string, modPath []string) (walkItem, bool) {
		t := p.ModuleIndex.target(in, currentModuleName, projectRoot, modPath)
		if len(t.rest) == 0 {
			return walkItem{}, false
		}
		return walkItem{module: t.module, root: t.root, modPath: t.rest, key: t.key}, true
	}

	queue := make([]walkItem, 0)
	for _, key := range roots {
		queue = append(queue, walkItem{root: projectRoot, modPath: strings.Split(key, "/"), key: key})
	}
	var stdBlocks *[][]string
	if stdBlock != nil {
		stdBlocks = new([][]string)
	}
	walk := func(nodes []ast.Node, std bool) [][]string {
		if stdBlocks == nil {
			return importsOf(nodes)
		}
		noteImportsWalk()
		paths := walkImportsInto(nodes, stdBlocks)
		for _, path := range *stdBlocks {
			stdBlock(strings.Join(path, "/"))
		}
		*stdBlocks = (*stdBlocks)[:0]
		for _, n := range nodes {
			fd, exported := n.(*ast.FuncDef)
			_, hostFn := n.(*ast.ExternFunc)
			if exported && fd.Export || hostFn && !std {
				// An exported function's arguments and result cross as
				// JSON, through std/json's FromJson and ToJson, which the
				// file need not import, and so may a program's own `host
				// fn`'s, to a host that answers it in JSON (a WASI
				// reactor's), which the checker records (HostJSONForms).
				stdBlock(stdJSONKey)
				break
			}
		}
		return paths
	}
	for _, mp := range walk(entryNodes, false) {
		if wi, ok := classify("", mp); ok {
			queue = append(queue, wi)
		}
	}
	if loader == nil {
		// No file is loaded; the entry's stdlib imports are still built
		// (StdSource), which a build without a loader resolves.
		for _, wi := range queue {
			if IsStdlibKey(wi.key) {
				analyzed(wi.key)
			}
		}
		return p, manifestErr
	}
	for len(queue) > 0 {
		wi := queue[0]
		queue = queue[1:]
		if _, ok := p.Files[wi.key]; ok {
			continue
		}
		if analyzed != nil && analyzed(wi.key) {
			continue
		}
		var imports [][]string
		if r, ok := reused[wi.key]; ok && wi.root == projectRoot {
			if p.reused[wi.key] {
				continue
			}
			if p.reused == nil {
				p.reused = map[string]bool{}
			}
			p.reused[wi.key] = true
			p.FilePaths[wi.key] = r.fa.FilePath
			imports = r.fa.importPaths(r.nodes)
		} else {
			nodes, err := loader(wi.root, wi.modPath)
			if err != nil || nodes == nil {
				continue
			}
			p.Files[wi.key] = nodes
			p.FilePaths[wi.key] = importDest{module: wi.module, root: wi.root, rest: wi.modPath}.file()
			imports = walk(nodes, IsStdlibKey(wi.key))
			if p.imports == nil {
				p.imports = map[string][][]string{}
			}
			p.imports[wi.key] = imports
		}
		// A file's imports name files through the names of the module
		// the file is in.
		for _, mp := range imports {
			// A stdlib file names its siblings bare; they are the same
			// module and every key in the toolchain spells it `std/<name>`.
			child, ok := classify(wi.module, QualifyIntraStdlibImport(wi.key, mp))
			if !ok {
				continue
			}
			queue = append(queue, child)
		}
	}
	return p, manifestErr
}

// importsOf returns the module path of every import in the given nodes: at
// file level, and, for project files only, at the top of any block beneath them (`fn main() {
// import shapes ... }`), since a file imported only there is as much a part
// of the program. Both ImportStmt and the entries of ImportBlock
// contribute. Stdlib paths are returned alongside user-module paths —
// post-cutover, stdlib walks the same BFS path as any other cross-module
// dep.
func importsOf(nodes []ast.Node) [][]string {
	noteImportsWalk()
	return walkImports(nodes)
}

// walkImports is importsOf without the count.
func walkImports(nodes []ast.Node) [][]string {
	return walkImportsInto(nodes, nil)
}

// walkImportsInto is walkImports that also appends to stdBlocks, when it is
// not nil, the path of each stdlib module imported in a block, which
// walkImports leaves out.
func walkImportsInto(nodes []ast.Node, stdBlocks *[][]string) [][]string {
	var paths [][]string
	for _, n := range nodes {
		paths = append(paths, importPathsOf(n)...)
		ast.Inspect(n, func(m ast.Node) bool {
			if m == n {
				return true
			}
			switch m.(type) {
			case *ast.ImportStmt, *ast.ImportBlock:
				// A stdlib module imported in a block stays with the
				// shared stdlib analysis, as it did before block imports
				// were followed; discovering it here breaks an aliased
				// block import of a std type (`import
				// std/duration.Duration as D` in a test, then
				// `D.minutes(1)`: "type 'D' has no member 'minutes'").
				for _, path := range importPathsOf(m) {
					if len(path) == 0 || path[0] != "std" {
						paths = append(paths, path)
					} else if stdBlocks != nil {
						*stdBlocks = append(*stdBlocks, path)
					}
				}
				return false
			}
			return true
		})
	}
	return paths
}

// importPathsOf returns the module paths of imports declared by a single
// top-level node — zero, one, or many depending on whether the node is
// a non-import, an ImportStmt, or an ImportBlock with multiple entries.
// Stdlib paths are returned alongside user-module paths — post-cutover,
// callers walk them through the same resolution path as any other dep.
func importPathsOf(node ast.Node) [][]string {
	switch s := node.(type) {
	case *ast.ImportStmt:
		return importStmtPaths(nil, s)
	case *ast.ImportBlock:
		out := make([][]string, 0, len(s.Entries))
		for _, entry := range s.Entries {
			out = importStmtPaths(out, entry)
		}
		return out
	}
	return nil
}

// importStmtPaths appends to out the path stmt imports and, after it, the
// path of each file its PathUses reach when it names a directory: the
// import's path with the use's segments after it. Whether it names a
// directory is resolution's to say; a path that names no file loads
// nothing.
func importStmtPaths(out [][]string, stmt *ast.ImportStmt) [][]string {
	path := importStmtPath(stmt)
	out = append(out, path)
	for _, use := range stmt.PathUses {
		out = append(out, append(slices.Clone(path), strings.Split(use, ast.PathSeparator)...))
	}
	return out
}

// importStmtPath extracts the module path segments from an ImportStmt.
// Mirrors the path-flattening defineImport / makeLoader uses.
func importStmtPath(stmt *ast.ImportStmt) []string {
	out := make([]string, len(stmt.ModulePath))
	for i, node := range stmt.ModulePath {
		out[i] = ast.ImportNodeName(node)
	}
	if len(stmt.Names) > 0 {
		for len(out) > 1 {
			if _, ok := stmt.ModulePath[len(out)-1].(*ast.TypeIdent); !ok {
				break
			}
			out = out[:len(out)-1]
		}
	}
	return out
}

// SelfNameImport reports whether modPath names a file of the current module
// through the module's own name (`import todo::foo` inside module `todo`), and
// answers the path within the module (`foo`). An import that is the module
// name alone (`import thing` inside module `thing`) is not one: a module is a
// directory, not a file, so that path names the file `thing.nomi` at the
// module root, as it would in a module of any other name.
func SelfNameImport(modPath []string, currentModuleName string) ([]string, bool) {
	if currentModuleName == "" || len(modPath) < 2 || modPath[0] != currentModuleName {
		return nil, false
	}
	return modPath[1:], true
}

// ImportFile answers the .nomi file that modPath, an import path written in a
// file of the project rooted at projectRoot (not `std/...`), names as the
// front end resolves it (ModuleIndex.target): through the project's own
// module name, a dependency's name (alone, its root file of that name), or
// a path within the project. It reads only the disk, and answers false when
// the file does not exist.
func ImportFile(projectRoot string, modPath []string) (string, bool) {
	if len(modPath) == 0 {
		return "", false
	}
	current := ""
	if m, err := LoadManifest(projectRoot); err == nil {
		current = m.Name
	}
	index, _ := BuildModuleIndex(projectRoot)
	return existingFile(index.target("", current, projectRoot, modPath))
}

// FileOfKey answers the .nomi file the program rooted at projectRoot keys
// key: a file of a dependency under the dependency's key name, or one of
// the project's own. It reads only the disk, and answers false when the file
// does not exist.
func FileOfKey(projectRoot, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	index, _ := BuildModuleIndex(projectRoot)
	t := importDest{root: projectRoot, rest: strings.Split(key, "/")}
	if m := index.moduleOfKey(key); m != "" {
		t.root, t.rest = index.Roots[m], t.rest[1:]
	}
	return existingFile(t)
}

// existingFile is the file t names, when it exists.
func existingFile(t importDest) (string, bool) {
	if len(t.rest) == 0 || t.root == "" {
		return "", false
	}
	path := t.file()
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// fileImports is importsOf of a file's nodes, kept with its analysis
// (FileAnalysis.importPaths) for the body part it was found for.
type fileImports struct {
	generation uint64
	paths      [][]string
}

// importPaths is importsOf(nodes), where nodes are the nodes fa was built
// from, as they are now: a dependents recheck walks the imports of every
// file it keeps from the program before, and their nodes have not changed
// since the last walk. The answer is kept with the analysis for its body
// part (FileBodies.generation), since a recheck that rebuilds the file's
// bodies rewrites its nodes in place, and the build that made fa keeps the
// imports its discovery found (keepImports).
func (fa *FileAnalysis) importPaths(nodes []ast.Node) [][]string {
	if m := fa.imports; m != nil && m.generation == fa.generation {
		if checkKeptImports {
			if walked := walkImports(nodes); !slices.EqualFunc(walked, m.paths, slices.Equal) {
				panic(fmt.Sprintf("analysis: the kept imports of %s are %v, and its nodes import %v", fa.FilePath, m.paths, walked))
			}
		}
		return m.paths
	}
	paths := importsOf(nodes)
	fa.imports = &fileImports{generation: fa.generation, paths: paths}
	return paths
}

// checkKeptImports makes importPaths walk the nodes on every answer it
// keeps and panic when the kept answer differs. This package's tests turn
// it on, so the incremental harness (TestIncrementalMatchesFull,
// FuzzIncrementalMatchesFull) holds the kept imports to the nodes; the
// scale benchmarks, which time the dependents path, leave it off.
var checkKeptImports bool

// keepImports keeps paths as importsOf of the nodes of fa, a build's
// analysis, for importPaths.
func (fa *FileAnalysis) keepImports(paths [][]string) {
	fa.imports = &fileImports{generation: fa.generation, paths: paths}
}

// carryImports keeps the imports fa's body part generation had for its
// current one: a recheck rebuilt its bodies, and the old and the new bodies
// import the same paths in the same order.
func (fa *FileAnalysis) carryImports(generation uint64) {
	if m := fa.imports; m != nil && m.generation == generation {
		fa.imports = &fileImports{generation: fa.generation, paths: m.paths}
	}
}
