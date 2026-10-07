package analysis

import (
	"errors"
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
	// ModuleIndex maps cross-module short-names (declared in sibling
	// modules' nomi.toml, reached via go.mod require + replace) to
	// their absolute filesystem roots. Empty when this project has no
	// go.mod, no cross-module deps, or only pure-Go deps. Consumed by
	// the builder's cross-module import resolver (resolveImport) and by
	// this file's discovery walk to follow imports into sibling
	// modules.
	ModuleIndex ModuleIndex
	// ImplIndex aggregates every reachable file's impl recordings
	// into a project-level view consumed by checker /
	// derive_synthesis / IR builder / collision detection. Populated
	// by BuildProjectWithCache post-Sweep-B. nil for the single-file
	// BuildFileWithStdlib path (consumers fall back to per-FA reads).
	ImplIndex *ProjectImplIndex
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
	p := &Project{
		Entry:       entryNodes,
		Files:       make(map[string][]ast.Node),
		FilePaths:   make(map[string]string),
		ProjectRoot: projectRoot,
		ModuleIndex: ModuleIndex{},
	}
	// Caller-supplied manifest wins (in-memory virtual mode). Otherwise
	// load nomi.toml once per build from disk. Missing manifest is fine —
	// single-file `nomi run` and many test fixtures have no manifest, and
	// downstream consumers skip when Manifest is nil. Malformed manifest
	// surfaces as an error to the caller (BuildProjectWithCache turns it
	// into a TypeError on the entry FA).
	if mfst != nil {
		p.Manifest = mfst
	} else if projectRoot != "" {
		if m, err := LoadManifest(projectRoot); err == nil {
			p.Manifest = m
		} else if !errors.Is(err, ErrManifestMissing) {
			return p, err
		}
	}
	// Build the cross-module index from go.mod. Missing go.mod returns
	// an empty index — single-file mode and no-deps projects keep
	// working. A genuine parse error or duplicate-name conflict
	// surfaces to the caller (same channel as a malformed manifest).
	if projectRoot != "" {
		idx, err := BuildModuleIndex(projectRoot)
		if err != nil {
			return p, err
		}
		p.ModuleIndex = idx
	}
	// Virtually inject stdlib as a module dependency under the
	// short-name "std". User go.mods don't declare it (every Nomi
	// program needs stdlib; making users write `require std v0.0.0`
	// is ceremony with no information value); the compiler locates
	// the bundled stdlib at process start via StdlibPath() and
	// pretends it was require'd. After this, `import std/strings`
	// resolves through the same b.moduleIndex path as any other
	// cross-module import — no special-case skipping below, no
	// std.Load() parallel pipeline. Injection failure (no bundled
	// std/ directory reachable) is non-fatal here so single-file
	// `nomi parse` flows that never touch stdlib still work; callers
	// that actually need stdlib will surface the failure when
	// resolveImport returns nil for `std/...` paths.
	if stdPath, err := StdlibPath(); err == nil {
		if p.ModuleIndex == nil {
			p.ModuleIndex = ModuleIndex{}
		}
		p.ModuleIndex["std"] = stdPath
	}
	if loader == nil {
		return p, nil
	}

	currentModuleName := ""
	if p.Manifest != nil {
		currentModuleName = p.Manifest.Name
	}

	// walkItem pairs an import path with the project root that owns it.
	// For intra-module imports root == projectRoot and modPath is the
	// raw import path. For cross-module imports root is the dependency
	// module's filesystem root and modPath is the rest of the import
	// after the cross-module short-name head.
	//
	// keyFor is the cache key the builder consumes via b.cache; it
	// always uses the user-facing import-path form so the builder's
	// resolveImport (which doesn't know about module roots) can look up
	// any walked file under the same key it computes from the import
	// AST.
	type walkItem struct {
		root    string
		modPath []string
		key     string
	}

	classify := func(modPath []string) (walkItem, bool) {
		if len(modPath) == 0 {
			return walkItem{}, false
		}
		key := strings.Join(modPath, "/")
		// Self-name reference: `import todo/foo` from within todo is
		// the same as `import foo` — strip the head and resolve
		// against the current root. The cache key follows what the
		// builder sees (the bare-name form), so a self-name import
		// and a sibling-of-it bare import deduplicate cleanly.
		if rest, self := SelfNameImport(modPath, currentModuleName); self {
			return walkItem{root: projectRoot, modPath: rest, key: strings.Join(rest, "/")}, true
		}
		if otherRoot, ok := p.ModuleIndex[modPath[0]]; ok {
			rest := modPath[1:]
			if len(rest) == 0 {
				return walkItem{}, false
			}
			return walkItem{root: otherRoot, modPath: rest, key: key}, true
		}
		return walkItem{root: projectRoot, modPath: modPath, key: key}, true
	}

	queue := make([]walkItem, 0)
	for _, mp := range importsOf(entryNodes) {
		if wi, ok := classify(mp); ok {
			queue = append(queue, wi)
		}
	}
	for len(queue) > 0 {
		wi := queue[0]
		queue = queue[1:]
		if _, ok := p.Files[wi.key]; ok {
			continue
		}
		nodes, err := loader(wi.root, wi.modPath)
		if err != nil || nodes == nil {
			continue
		}
		p.Files[wi.key] = nodes
		p.FilePaths[wi.key] = ResolveModulePath(wi.root, wi.modPath)
		// Sibling-module imports inside a freshly-loaded sibling
		// module are walked relative to ITS module index, not the
		// importing project's. Only one-hop deps are supported
		// (entry's go.mod is the index), so siblings' transitive
		// imports re-use the entry's currentModuleName / module index
		// when classifying — practical effect: the sibling can still
		// reach back into the entry's module by short-name, can reach
		// any sibling-of-sibling listed in the entry's go.mod, and
		// any import outside that union falls through to intra-module
		// resolution against the sibling's own root.
		for _, mp := range importsOf(nodes) {
			// A stdlib file names its siblings bare; they are the same
			// module and every key in the toolchain spells it `std/<name>`.
			child, ok := classify(QualifyIntraStdlibImport(wi.key, mp))
			if !ok {
				continue
			}
			// Intra-module imports inside a sibling resolve against the
			// SIBLING's root, not the entry's.
			if child.root == projectRoot && wi.root != projectRoot {
				// Heuristic: if classify routed it back to projectRoot
				// (no head match), it's an intra-module path from the
				// sibling's POV — re-anchor to the sibling's root.
				child.root = wi.root
			}
			queue = append(queue, child)
		}
	}
	return p, nil
}

// importsOf returns the module path of every import in the given nodes: at
// file level, and, for project files only, at the top of any block beneath them (`fn main() {
// import shapes ... }`), since a file imported only there is as much a part
// of the program. Both ImportStmt and the entries of ImportBlock
// contribute. Stdlib paths are returned alongside user-module paths —
// post-cutover, stdlib walks the same BFS path as any other cross-module
// dep.
func importsOf(nodes []ast.Node) [][]string {
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
		return [][]string{importStmtPath(s)}
	case *ast.ImportBlock:
		out := make([][]string, 0, len(s.Entries))
		for _, entry := range s.Entries {
			out = append(out, importStmtPath(entry))
		}
		return out
	}
	return nil
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
// through the module's own name (`import todo/foo` inside module `todo`), and
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
