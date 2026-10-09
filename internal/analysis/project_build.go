package analysis

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// BuildProject is the project-wide build entry point. Given an entry
// file's parsed nodes, it discovers every reachable user-module file,
// runs three sweeps across all of them, and returns the entry file's
// FileAnalysis.
//
// The three sweeps:
//
//	Sweep A — register top-level symbols across every file
//	          (defineSymbolStub). After this, every symbol in the
//	          project is registered somewhere.
//
//	Sweep B — walk type annotations and function bodies across every
//	          file (defineSymbolAnnotations + buildModuleBodies).
//	          Because Sweep A finished, qualified-name lookups
//	          (e.g. iter.Iter<T> from another file) succeed
//	          regardless of file order.
//
//	Sweep C — BuildTypes for each file. The TypeRegistry consulted by
//	          ResolveTypeExpr is populated from each file's now-fully-
//	          populated ModuleScope.
//
// Cyclic imports between user files resolve naturally — by Sweep B
// every file's symbols are visible. A separate per-file
// BuildFileWithStdlib path is available for stdlib loading and for
// any callers that don't have a project context.
//
// CheckTypes is NOT run here — callers (DocumentManager.analyze,
// internal/frontend's Checker.Analyze) layer it on top.
func BuildProject(entryNodes []ast.Node, primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis, projectRoot string, loader FileLoader) *FileAnalysis {
	entryFA, _, _ := buildProjectWithCache(entryNodes, primitives, modules, stdlibFAs, projectRoot, "", "", false, loader, nil)
	return entryFA
}

// BuildProjectWithCache is BuildProject's variant that also returns the
// per-module FileAnalysis cache and the per-module post-derive node
// slices, both keyed by "/" -joined module path (e.g. "greeter",
// "config/prod").
//
// The cache is used by the LSP so that when the open document isn't the
// project entry — main.nomi imports greeter.nomi but greeter.nomi's
// imports never reach main.nomi — we can build the project from the
// actual entry (so the import graph walk reaches every sibling,
// including the open doc) and still hand the open doc's own
// FileAnalysis back to the editor.
//
// The sibling-nodes map exists so callers that layer CheckTypes on top
// (internal/frontend's Checker.Analyze, stdcompiler) can run it
// over every sibling FA + nodes pair, not just the entry. CheckTypes
// records conformance facts (StringInterp, generic-bound, impl, …)
// onto its FA's ImplManifest as a side effect; without per-sibling
// runs, recordings inside sibling function bodies (e.g. `${name}` in
// `pub fn greet(name) { "hi, ${name}" }`) never fire and the entry's
// ImplManifest stays incomplete. Callers that don't need the per-
// sibling recordings (the LSP's per-document path, test helpers that
// only inspect the entry) can ignore this map.
//
// The entry file's FA is NOT placed in the returned cache, and the
// entry's nodes are NOT placed in the returned nodes map; callers that
// want them use the first return value (entry FA) and the nodes they
// passed in.
func BuildProjectWithCache(entryNodes []ast.Node, primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis, projectRoot string, loader FileLoader) (*FileAnalysis, map[string]*FileAnalysis, map[string][]ast.Node) {
	return buildProjectWithCache(entryNodes, primitives, modules, stdlibFAs, projectRoot, "", "", false, loader, nil)
}

// BuildProjectFromEntry is BuildProjectWithCache plus the entry
// file's absolute path. The entry's path lets the analyzer set
// FileAnalysis.FilePath for the entry, which downstream checks
// (internal/ access, entry-file enforcement) consume to derive the
// entry's module-relative path. Callers that don't know the entry
// path (LSP per-document analysis, tests) keep using
// BuildProjectWithCache and accept the empty FilePath for the entry.
func BuildProjectFromEntry(entryAbsPath string, entryNodes []ast.Node, primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis, projectRoot string, loader FileLoader) (*FileAnalysis, map[string]*FileAnalysis, map[string][]ast.Node) {
	return buildProjectWithCache(entryNodes, primitives, modules, stdlibFAs, projectRoot, entryAbsPath, "", false, loader, nil)
}

// BuildProjectFromEntryWithManifest is BuildProjectFromEntry plus a
// pre-parsed Manifest and the entry's module-relative path. Used by
// callers staging a project in memory (tour playground, doctest
// harness, embedders) so the manifest-driven checks — entry_points
// placement, internal/ access, orphan-rule module identity — fire
// even when there's no nomi.toml on disk. `entryModRel` is the entry
// file's path *under the module root* without the `.nomi` extension
// (e.g. "main", "tools/seed"); pass "" when the caller doesn't know
// it. The key is the entry's identity in the import graph: a file
// that imports the entry back reaches the entry's own analysis, not
// a second copy of the file. placeEntry runs the entry-side
// CheckEntryPlacement under that key; a test file passes false,
// since it is not an entry point. When mfst is nil this behaves
// identically to BuildProjectFromEntry.
func BuildProjectFromEntryWithManifest(entryAbsPath string, entryNodes []ast.Node, primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis, projectRoot string, loader FileLoader, mfst *Manifest, entryModRel string, placeEntry bool) (*FileAnalysis, map[string]*FileAnalysis, map[string][]ast.Node) {
	return buildProjectWithCache(entryNodes, primitives, modules, stdlibFAs, projectRoot, entryAbsPath, entryModRel, placeEntry, loader, mfst)
}

func buildProjectWithCache(entryNodes []ast.Node, primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis, projectRoot string, entryAbsPath string, entryModRel string, placeEntry bool, loader FileLoader, mfst *Manifest) (*FileAnalysis, map[string]*FileAnalysis, map[string][]ast.Node) {
	proj, discoverErr := DiscoverProjectWithManifest(entryNodes, projectRoot, loader, mfst)
	if entryAbsPath != "" {
		proj.EntryPath = entryAbsPath
		if proj.FilePaths == nil {
			proj.FilePaths = make(map[string]string)
		}
		proj.FilePaths[""] = entryAbsPath
	}
	if entryModRel != "" {
		delete(proj.Files, entryModRel)
		delete(proj.FilePaths, entryModRel)
	}

	cache := make(map[string]*FileAnalysis)
	loading := make(map[string]bool)

	type fileBuilder struct {
		key     string // module-path key (empty for entry file)
		nodes   []ast.Node
		fa      *FileAnalysis
		builder *builder
	}
	all := make([]*fileBuilder, 0, len(proj.Files)+1)

	makeBuilder := func(nodes []ast.Node, key string) *fileBuilder {
		// Stdlib files don't get the prelude as a parent scope: they
		// import what they need explicitly, and a prelude-as-parent
		// would trip checkReservedTypeName when `enum Result {...}`
		// in std/results.nomi sees `Result` already-defined in prelude
		// (which re-exported it from this very file in a prior load).
		// Equivalent to what std.Load()'s loadModule() did pre-cutover
		// — it passed a bare bootstrap scope as the parent, not the
		// prelude scope.
		fileParent := primitives
		if IsStdlibKey(key) {
			fileParent = nil
		}
		moduleScope := NewScope(fileParent)
		// FilePath uses the same ResolveModulePath the import-side
		// propagation uses to populate sym.SourceFile, so an opaque
		// type's OwningSourceFile (copied from sym.SourceFile during
		// type build) compares cleanly against c.fa.FilePath inside the
		// type's owning module. Empty for the project entry; the entry
		// has no key and its symbols are never written to by import
		// propagation, leaving OwningSourceFile blank for any opaque
		// types it declares — which matches its empty FilePath here.
		filePath := proj.FilePaths[key]
		fa := &FileAnalysis{
			ModuleScope: moduleScope,
			FilePath:    filePath,
			Origin:      originForKey(key),
			References:  make(map[Pos]*Symbol),
			Definitions: make(map[Pos]*Symbol),
		}
		// Per-file modules map: every discovered project + stdlib file
		// goes in first (so its ModuleScope wins for cross-file
		// qualified-type lookups like `iter.Iter`), then any extra
		// scopes the caller passed in via `modules` fill remaining
		// slots. The `modules` arg from std.Load()
		// is redundant (stdlib lives in cache), but it's still accepted
		// for the legacy `BuildFileWithStdlib` single-file path —
		// preferring cache over `modules` makes the redundant arg
		// harmless rather than divergent.
		//
		// SNAPSHOT-AT-CONSTRUCTION TIMING: `localModules` reads `cache`
		// once at makeBuilder construction time. `cache` is mutated by
		// subsequent makeBuilder calls (each fileBuilder writes its own
		// fa into cache[key] at line 182 below, and each buildModule
		// pass writes the fully-built FA back), so:
		//   - A file built LATER that depends on a file built EARLIER
		//     in this same makeBuilder pass DOES see the earlier file
		//     in its localModules (the earlier file's makeBuilder call
		//     already wrote into cache before this call ran).
		//   - Two files built in the SAME makeBuilder pass DON'T see
		//     each other in localModules unless the build order places
		//     dependencies first.
		// The buildOrder loops in buildProjectWithCache (the stdlib-
		// first stdlibBuildOrder loop + the user-file loop that follows)
		// preserve this invariant: stdlib is processed first so cross-
		// file qualified-type lookups inside stdlib resolve, then user
		// files build in `proj.Files` map iteration order — which is
		// fine for the orphan rule / impl coherence (those run later
		// over filesByKey, post-sweep) but means a user-file
		// `alpha.nomi` doing `import beta.{X}` only sees `X` if beta
		// was already built. A future refactor reordering these loops
		// must preserve the dependency-first invariant or this snapshot
		// behaviour silently breaks cross-user-file qualified lookups.
		localModules := make(map[string]*Scope, len(modules)+len(cache))
		moduleSyms := make(map[string]*Symbol, len(modules)+len(cache))
		for k, otherFA := range cache {
			seg := lastSegment(k)
			if seg == "" {
				continue
			}
			localModules[seg] = otherFA.ModuleScope
			moduleSyms[seg] = &Symbol{Name: seg, Kind: SymbolModule, Pos: Pos{Line: 1, Col: 1}}
		}
		for name, scope := range modules {
			if _, exists := localModules[name]; exists {
				continue
			}
			moduleSyms[name] = &Symbol{Name: name, Kind: SymbolModule, Pos: Pos{Line: 1, Col: 1}}
			localModules[name] = scope
		}
		currentModuleName := ""
		if proj != nil && proj.Manifest != nil {
			currentModuleName = proj.Manifest.Name
		}
		var moduleIndex ModuleIndex
		if proj != nil {
			moduleIndex = proj.ModuleIndex
		}
		b := &builder{
			file:              fa,
			modules:           localModules,
			moduleSyms:        moduleSyms,
			loader:            loader,
			projectRoot:       projectRoot,
			moduleAlias:       moduleAliasForFile(key, filePath),
			currentModuleName: currentModuleName,
			moduleIndex:       moduleIndex,
			cache:             cache,
			loading:           loading,
			stdlibFAs:         stdlibFAs,
		}
		if key != "" {
			cache[key] = fa
		}
		return &fileBuilder{key: key, nodes: nodes, fa: fa, builder: b}
	}

	// Auto-prepend the prelude import chain to every non-stdlib file.
	// The legacy std.Load()-driven path made prelude's ModuleScope the
	// parent scope of every user file, so bare lookups for Some/None/Equal/...
	// walked through prelude's Maybe/Ordering enums and found them as
	// Members. Stdlib flows through the regular
	// discovery pipeline so prelude is just another module, and the
	// right way to surface its re-exported names is the same `import`
	// statement a user would write by hand. Stdlib files opt out —
	// they import what they need explicitly (a prelude inject inside
	// std/strings.nomi would re-import String, which is declared there).
	//
	// preludeImports() parses prelude.nomi once and returns its
	// re-export `import` chain with `export` flags stripped — mirroring
	// prelude.nomi's drill-through structure (e.g. `std/maybe.Maybe.{self,
	// None, Some}`) is what lets the variant names land in user scope
	// without tripping the "cannot import variant 'X' directly" check.
	preludeStmts, preludeErr := preludeImports(proj.ModuleIndex["std"], loader)
	if len(preludeStmts) > 0 {
		for key, nodes := range proj.Files {
			if IsStdlibKey(key) {
				continue
			}
			proj.Files[key] = prependPreludeImports(nodes, preludeStmts)
		}
		// Entry file is always non-stdlib (an entry under std/ would be
		// reached via discovery as a sibling key, not as the entry).
		entryNodes = prependPreludeImports(entryNodes, preludeStmts)
	}

	// Run @derive synthesis on every file's nodes before any sweep sees
	// them. Synthesized impl-block nodes flow through Sweep A's
	// defineSymbolStub like any hand-written impl block, so the analyzer's
	// Impls / DispatchNames tables capture them automatically. Errors
	// surface on each file's FileAnalysis.TypeErrors below.
	//
	// IMPORTANT: synthesis also happens in the front end
	// (internal/frontend's Checker.Prepare calls SynthesizeDerives once
	// before analysis). This block is the analysis-only entrypoint — when callers go
	// straight to BuildProject (e.g. the LSP) we still want the same
	// synthesis behaviour.
	deriveErrsByKey := make(map[string][]TypeError, len(proj.Files)+1)
	for key, nodes := range proj.Files {
		// Derive lowering FIRST: derive declarations become top-level impl
		// blocks, which the derive + universal-Debug passes
		// below (and Sweep A's
		// defineSymbolStub) then see exactly like hand-written impls.
		// Errors are internal parser-regression guards; surface them
		// alongside the derive errors.
		lowered, lowerErrs := LowerDerives(nodes)
		extended, errs := SynthesizeDerives(lowered)
		errs = append(lowerErrs, errs...)
		// Universal default Debug: after derive synthesis, eagerly synthesize
		// an `impl Debug` block for every declared type lacking ANY explicit Debug.
		// Per-file (so the orphan rule attributes each auto-impl to its
		// receiver type's home module) and after SynthesizeDerives (so an
		// explicit `@derive Debug` suppresses the auto one). The synthesized
		// `impl Debug` blocks flow through Sweep A's defineSymbolStub like
		// any other impl, landing in the Impls / dispatch tables.
		extended = SynthesizeUniversalDebug(extended)
		proj.Files[key] = extended
		if len(errs) > 0 {
			deriveErrsByKey[key] = errs
		}
	}
	loweredEntry, entryLowerErrs := LowerDerives(entryNodes)
	extendedEntry, entryDeriveErrs := SynthesizeDerives(loweredEntry)
	entryDeriveErrs = append(entryLowerErrs, entryDeriveErrs...)
	extendedEntry = SynthesizeUniversalDebug(extendedEntry)
	entryNodes = extendedEntry

	// Build discovered modules' file builders first so their ModuleScopes
	// land in the cache; then build the entry's so it sees every other
	// module by name.
	//
	// Order: stdlib files first (in the canonical StdlibLoadOrder, plus
	// prelude after — which discovery reaches via auto-prepended imports
	// on user files), then user files in map order, then the entry file
	// last. Pre-cutover, stdlib was fully built before BuildProject ran
	// so user files always saw populated stdlib scopes; post-cutover
	// stdlib goes through the same Sweep A/B/C path and a `Context`
	// symbol resolution in a user file's `import std/context.Context`
	// would observe an empty std/context FA if Sweep A hadn't reached
	// std/context yet. Ordering stdlib before user files restores the
	// invariant.
	stdlibBuildOrder := make([]string, 0, len(StdlibLoadOrder)+1)
	seenStdlib := make(map[string]bool, len(proj.Files))
	appendNested := func(root string) {
		prefix := root + "/"
		var nested []string
		for key := range proj.Files {
			if !IsStdlibKey(key) || !strings.HasPrefix(key, prefix) {
				continue
			}
			nested = append(nested, key)
		}
		sort.Slice(nested, func(i, j int) bool {
			depthI := strings.Count(nested[i], "/")
			depthJ := strings.Count(nested[j], "/")
			if depthI != depthJ {
				return depthI > depthJ
			}
			return nested[i] < nested[j]
		})
		for _, key := range nested {
			if !seenStdlib[key] {
				stdlibBuildOrder = append(stdlibBuildOrder, key)
				seenStdlib[key] = true
			}
		}
	}
	for _, name := range StdlibLoadOrder {
		root := "std/" + name
		appendNested(root)
		stdlibBuildOrder = append(stdlibBuildOrder, root)
		seenStdlib[root] = true
	}
	stdlibBuildOrder = append(stdlibBuildOrder, "std/prelude")
	seenStdlib["std/prelude"] = true
	// Stdlib files in canonical order.
	for _, key := range stdlibBuildOrder {
		if nodes, ok := proj.Files[key]; ok {
			all = append(all, makeBuilder(nodes, key))
		}
	}
	// User files (and any stdlib files not in the canonical order — none
	// expected today, but the fallback keeps the loop honest if someone
	// adds a stdlib module without updating stdlibBuildOrder above).
	// Sorted, so every build of a program builds its files in one order and
	// a defect that depends on that order shows on every run or none.
	userKeys := make([]string, 0, len(proj.Files))
	for key := range proj.Files {
		if IsStdlibKey(key) && seenStdlib[key] {
			continue
		}
		userKeys = append(userKeys, key)
	}
	sort.Strings(userKeys)
	for _, key := range userKeys {
		all = append(all, makeBuilder(proj.Files[key], key))
	}
	entryFB := makeBuilder(entryNodes, "")
	if entryModRel != "" {
		cache[entryModRel] = entryFB.fa
	}
	all = append(all, entryFB)
	// Surface manifest-load failures (e.g. malformed nomi.toml) as a
	// TypeError on the entry FA. Missing nomi.toml is not an error —
	// DiscoverProject swallows ErrManifestMissing and leaves
	// proj.Manifest nil so single-file mode still works.
	//
	// Line/Col 1,1 is a placeholder: the error originates in nomi.toml,
	// not in any .nomi source, so there is no meaningful Nomi-source
	// position to point at. The error message itself names the file
	// (LoadManifest's errors include the manifest path).
	if discoverErr != nil {
		entryFB.fa.TypeErrors = append(entryFB.fa.TypeErrors, TypeError{
			Line: 1, Col: 1,
			Message: discoverErr.Error(),
		})
	}
	if preludeErr != nil {
		// Surface a parse failure in std/prelude.nomi as a single
		// build-blocking diagnostic on the entry FA — without this, a
		// silent swallow would produce mass "undefined name" errors
		// across every user file (prelude provides their implicit
		// imports) with no pointer back to the cause. Line/Col 1,1 is
		// a placeholder; the error message itself carries the prelude-
		// internal line/col.
		entryFB.fa.TypeErrors = append(entryFB.fa.TypeErrors, TypeError{
			Line: 1, Col: 1,
			Message: preludeErr.Error(),
		})
	}
	// Enforce nomi.toml's entry_points contract: every declared entry
	// point must have fn main; non-entry files must not define fn main. The check
	// noops when proj.Manifest is nil (single-file mode, ad-hoc test
	// fixtures), so no behavior change for callers without a manifest.
	// entryModRel comes from the caller (BuildProjectFromEntryWithManifest):
	// virtual-mode runs supply it from the FILE marker's base name, and
	// disk-based callers may still pass "" to skip the entry-side check, as
	// a test file's caller does with placeEntry false.
	placedEntry := ""
	if placeEntry {
		placedEntry = entryModRel
	}
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors,
		CheckEntryPlacement(proj.Manifest, entryNodes, placedEntry, proj.Files)...,
	)
	// derive Attach validation errors to each file's FileAnalysis now
	// that the per-file FA exists.
	entryFB.fa.TypeErrors = append(entryFB.fa.TypeErrors, entryDeriveErrs...)
	for _, fb := range all {
		if fb.key == "" {
			continue
		}
		if errs, ok := deriveErrsByKey[fb.key]; ok {
			fb.fa.TypeErrors = append(fb.fa.TypeErrors, errs...)
		}
	}

	// Seed ContextType on every FileAnalysis with whatever the passed-in
	// `modules` arg can offer up-front. After Sweep C-types runs (below)
	// we re-seed from the cache so the value points at the freshly-built
	// std/context FA's Context type — see the longer comment at the
	// re-seed site. The early seed exists for two-pass consumers that
	// touch ContextType during the sweeps; nothing in the project build
	// itself reads it before re-seed, but downstream check passes that
	// run on the same fb.fa do.
	earlyContextType := findContextType(modules)
	// StdlibModuleScopes: short stdlib name → defining module Scope,
	// broadcast to every FA (same pattern as ContextType) so the iter-
	// sensitivity pass can discover stdlib module scopes — stdlib symbols
	// carry no SourceFile, which is what its SourceFile-based discovery
	// keys on. Prefer the cache's freshly-built "std/<name>" FAs (their
	// scopes are what import resolution hands user files post-cutover —
	// the Scope pointers are stable even though Sweep A hasn't filled
	// them yet); the legacy `modules` arg fills any gaps.
	stdlibScopes := make(map[string]*Scope)
	for key, cfa := range cache {
		if !IsStdlibKey(key) || cfa == nil || cfa.ModuleScope == nil {
			continue
		}
		if seg := lastSegment(key); seg != "" {
			stdlibScopes[seg] = cfa.ModuleScope
		}
	}
	for name, scope := range modules {
		if _, ok := stdlibScopes[name]; !ok {
			stdlibScopes[name] = scope
		}
	}
	for _, fb := range all {
		fb.fa.ContextType = earlyContextType
		fb.fa.StdlibModuleScopes = stdlibScopes
	}

	// Sweep A: register top-level symbols.
	for _, fb := range all {
		fb.builder.fileScope = fb.fa.ModuleScope
		for _, node := range fb.nodes {
			fb.builder.defineSymbolStub(node, fb.fa.ModuleScope)
		}
		fb.builder.defineImplicitSelfModule(fb.fa.ModuleScope)
		fb.builder.validateInlineOpaqueDecls(fb.nodes)
	}

	// Sweep B-ann: walk type annotations across every file. This
	// populates each file's local Impls table via
	// defineImplBlockAnnotations. Cross-file `mergeImpls` calls that
	// happen during defineImport→resolveImport may see partial Impls
	// for siblings whose annotations haven't run yet — Sweep B-impls
	// below corrects that.
	for _, fb := range all {
		for _, node := range fb.nodes {
			fb.builder.defineSymbolAnnotations(node, fb.fa.ModuleScope)
		}
		// Interface default-method registration: now that every impl block in
		// this file has been defined, register any interface defaults that no
		// user method overrides. Per-file: the
		// override set is local to each file's builder.
		fb.builder.registerImplDefaults(fb.fa.ModuleScope)
	}

	// Sweep B-impls: propagate impls across import edges. Every file's
	// local Impls is now fully populated, so re-merging each file's
	// imports' Impls produces the full transitive conformance graph
	// regardless of the order Sweep B-ann processed files. Stdlib
	// imports are looked up the same way as any other import — when a
	// stdlib file is present in `cache` (either because the user wrote
	// `import std/X` and discovery loaded it, or because Sweep A/B
	// pulled it in on-demand), its impls fold in here too. Stdlib
	// impls that don't reach the cache via this transitive merge are
	// still picked up downstream — the project-level ProjectImpls
	// index folds every reachable stdlib FA via the eager fold
	// below, so the checker's ProjectImpls branch covers the gap.
	//
	// Cache-key normalization mirrors resolveImport: a self-name
	// import (head == currentModuleName) caches under the stripped
	// rest-of-path; cross-module and intra-module bare imports cache
	// under the raw import path. Walking imports through the same
	// classifier keeps this loop's lookup in lockstep with what
	// resolveImport actually wrote to cache.
	currentModuleName := ""
	if proj != nil && proj.Manifest != nil {
		currentModuleName = proj.Manifest.Name
	}
	for _, fb := range all {
		for _, node := range fb.nodes {
			for _, modPath := range importPathsOf(node) {
				if len(modPath) == 0 {
					continue
				}
				// A stdlib file's bare sibling import is the same
				// module; resolveImport keyed it `std/<name>`.
				modPath = QualifyIntraStdlibImport(fb.key, modPath)
				key := strings.Join(modPath, "/")
				if rest, self := SelfNameImport(modPath, currentModuleName); self {
					key = strings.Join(rest, "/")
				}
				if key == "" {
					continue
				}
				if cached, ok := cache[key]; ok {
					mergeImpls(fb.fa, cached)
					mergeImplManifest(fb.fa, cached)
				}
			}
		}
	}

	// Project-wide IfaceMethodImpls index. Walks every file's `impl Iface for
	// T { ... }` blocks (IndexImplBlockFuncDefs) and records each method
	// FuncDef under (ifaceName → methodName → []FuncDef). Broadcast to every
	// file's FA so a checker running in any file sees the same project-wide
	// view — same broadcast pattern as AppType / ContextType. fb.key is
	// the import-form module key ("foo" or "sub/log"; "" for the project
	// entry file).
	ifaceImpls := make(map[string]map[string][]*ast.FuncDef)
	ifaceImplFiles := make(map[*ast.FuncDef]string)
	// implBlockReceivers maps each block-form impl method FuncDef to its
	// header receiver base name — coherence keys on this because a block
	// item's first parameter is `self` (unresolvable by the first-param
	// heuristic). inherentImpls collects inherent-block methods for the
	// inherent method index and duplicate function checks; inherentBlocks
	// collects source blocks for duplicate-block + same-file receiver checks.
	implBlockReceivers := make(map[*ast.FuncDef]string)
	implBlockInterfaceKeys := make(map[*ast.FuncDef]string)
	// Parallel extern index for block-form `host fn` interface impls — the
	// items the FuncDef maps above exclude. Carries them so go-to-def / hover
	// resolve an extern impl to its concrete source (see FileAnalysis docs).
	ifaceImplExterns := make(map[string]map[string][]*ast.ExternFunc)
	ifaceImplExternFiles := make(map[*ast.ExternFunc]string)
	implBlockInterfaceKeysExtern := make(map[*ast.ExternFunc]string)
	var inherentImpls []InherentMethodRecord
	var inherentBlocks []InherentImplBlockRecord
	declaredTypesByFile := make(map[string]map[string]bool)
	for _, fb := range all {
		IndexImplBlockFuncDefs(fb.nodes, fb.key, fb.fa.Definitions, ifaceImpls, ifaceImplFiles, implBlockReceivers, implBlockInterfaceKeys, ifaceImplExterns, ifaceImplExternFiles, implBlockInterfaceKeysExtern, &inherentImpls)
		collectInherentImplBlocks(fb.nodes, fb.key, &inherentBlocks)
		names := make(map[string]bool)
		collectDeclaredTypeNames(fb.nodes, names)
		declaredTypesByFile[fb.key] = names
	}
	// Stdlib files first, then this build's own files (which win on any
	// key overlap). An impl whose home is stdlib must still find its
	// FileAnalysis here, or its receiver loses the module qualifier and
	// a project type of the same name looks like a duplicate impl.
	faByKey := make(map[string]*FileAnalysis, len(all)+len(cache))
	for key, fa := range cache {
		faByKey[key] = fa
	}
	for _, fb := range all {
		faByKey[fb.key] = fb.fa
	}
	qualifiedImplBlockReceivers := make(map[*ast.FuncDef]string, len(implBlockReceivers))
	for fn, recv := range implBlockReceivers {
		// nil primitives: this pass runs before BuildTypes, so it yields bare
		// names for everything regardless, and PopulateQualifiedReceivers
		// overwrites every entry below once types exist.
		qualifiedImplBlockReceivers[fn] = qualifyReceiver(faByKey[ifaceImplFiles[fn]], recv, nil)
	}
	for _, fb := range all {
		fb.fa.IfaceMethodImpls = ifaceImpls
		fb.fa.IfaceMethodImplFiles = ifaceImplFiles
		// Broadcast the canonical block-header receiver map alongside the
		// method index. The per-file maps are not guaranteed to contain the
		// FuncDefs from the shared index (stdlib FAs can be reached through
		// both the build and cache paths), so leaving this local would make
		// identity population depend on which FA the index fold visits.
		fb.fa.ImplBlockReceiver = implBlockReceivers
		fb.fa.ImplBlockInterfaceKey = implBlockInterfaceKeys
		fb.fa.IfaceMethodImplExterns = ifaceImplExterns
		fb.fa.IfaceMethodImplExternFiles = ifaceImplExternFiles
		fb.fa.ImplBlockInterfaceKeyExtern = implBlockInterfaceKeysExtern
	}

	// filesByKey folds two sources together: `all` (every file in
	// `proj.Files` + the entry) and `cache` (files loaded on-demand
	// during the sweeps via resolveImport — notably stdlib files reached
	// through the auto-prepended `import std/prelude` chain, which
	// discovery walked PAST because the auto-prepend happens AFTER
	// DiscoverProject returns). Built here so the project impl index
	// (below) and downstream consumers (orphan check, etc.) share one
	// view of every reachable FA.
	filesByKey := make(map[string]*FileAnalysis, len(all)+len(cache)+len(stdlibFAs))
	for _, fb := range all {
		filesByKey[fb.key] = fb.fa
	}
	for key, fa := range cache {
		if _, present := filesByKey[key]; present {
			continue
		}
		filesByKey[key] = fa
	}
	// Eagerly fold every pre-analyzed stdlib FA into filesByKey so the
	// project impl index covers stdlib uniformly regardless of which
	// modules the entry program transitively imported. The prelude's
	// re-export chain pulls a handful of stdlib modules into discovery /
	// cache via the auto-prepend, but the rest don't appear in cache —
	// their `impl` blocks live in the module file itself and nothing in
	// prelude imports them. Without this fold, a program that only ever
	// mentions a prelude-promoted name would find no project-index entry
	// for that name's stdlib impls because the home module never reached
	// cache. stdlibFAs (lib.Files) already carries every analyzed stdlib
	// FA, so this fold is effectively free — just an extra "stdlib home"
	// union before the index walk.
	for name, fa := range stdlibFAs {
		key := "std/" + name
		if _, present := filesByKey[key]; present {
			continue
		}
		filesByKey[key] = fa
	}
	nodesByKey := make(map[string][]ast.Node, len(all))
	for _, fb := range all {
		nodesByKey[fb.key] = fb.nodes
	}
	// Project-level impl index: single source of truth for "which Ts
	// implement which Ifaces" across the whole reachable program.
	// Attached to proj for project-level consumers (checker, derive
	// synthesis) and broadcast to every FA in filesByKey — that union
	// covers `all` (entry + discovery-walked siblings), `cache` (on-
	// demand-resolved siblings, plus every stdlib FA reached via
	// resolveStdlibImport's write-through), and the stdlibFAs eager
	// fold above (every other stdlib FA the prelude chain didn't
	// touch). Broadcasting to cache FAs too matters because the
	// FA-mediated pattern is used by several consumers (LSP, derive
	// synthesis) and a uniform back-pointer avoids surprises when a
	// non-entry FA flows into one of them.
	//
	// Order matters: this runs BEFORE Sweep B-bounds below, so
	// CheckDeriveBounds sees a populated fa.ProjectImpls. Inputs
	// (fa.Impls, fa.ImplManifest, fa.IfaceMethodImpls) are all
	// populated by Sweep B-impls (just above) plus std.Load() for
	// stdlib FAs.
	implIndex := buildProjectImplIndex(filesByKey)
	if proj != nil {
		proj.ImplIndex = implIndex
	}
	// Only the files THIS build owns get their pointer rebound. The
	// stdlib FileAnalysis objects in filesByKey are shared across every
	// Runtime in the process, so rebinding them lets one build hand a
	// later, unrelated build an index assembled for a different project
	// — which showed up as an intermittent dispatch-collision panic
	// where a stdlib impl lost its module qualifier. A shared FA still
	// gets an index the first time it needs one, since analyzing a
	// stdlib file as a subject goes through AttachStdlibProjectImpls.
	owned := make(map[*FileAnalysis]bool, len(all))
	for _, fb := range all {
		owned[fb.fa] = true
	}
	for _, fa := range filesByKey {
		if owned[fa] || fa.ProjectImpls == nil {
			fa.ProjectImpls = implIndex
		}
	}

	// Sweep B-bounds: the derive field/payload bound check (spec §38.1,
	// *Field-type requirements*). Runs
	// after Sweep B-impls so every file's local Impls is fully
	// populated, and AFTER the project impl index has been broadcast
	// onto every FA so CheckDeriveBounds can consult fa.ProjectImpls
	// for cross-file / stdlib impl knowledge. The (T, Iface) entries
	// for the @derive'd types themselves are also present (registered
	// by defineImplBlockStub on the synthesizer's emitted
	// impl block during Sweep A/B-ann), letting recursive types
	// pass their own self-reference check naturally.
	for _, fb := range all {
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, CheckDeriveBounds(fb.fa, fb.nodes)...)
	}

	// Sweep B-bodies: walk function bodies. By now every file's
	// symbols and impls are visible, so cross-file references in
	// expressions resolve cleanly.
	for _, fb := range all {
		fb.builder.buildModuleBodies(fb.nodes, fb.fa.ModuleScope)
	}

	// Unused checks: run immediately after Sweep B-bodies because
	// that sweep completes fa.References (annotations + impl headers +
	// bounds + @derive args landed in Sweep A/B-ann; expression, pattern,
	// and typed-literal-tag references land here) and BEFORE CheckTypes,
	// which rewrites References entries with per-call-site proxy copies.
	// Single-file callers get the same checks at the tail of buildModule;
	// on-demand resolveImport loads go through buildModule too, so files
	// in `cache` but not `all` are already covered.
	for _, fb := range all {
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, CheckUnusedImports(fb.fa, fb.nodes)...)
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, CheckRedundantPreludeImports(fb.fa, fb.nodes)...)
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, CheckUnusedBindings(fb.fa)...)
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, CheckUselessReturns(fb.nodes)...)
	}

	// Sweep C-shells: pre-populate every type symbol's `.Type` with a
	// shell pointer (empty struct/enum/distinct, final interface/extern).
	// BuildTypes for one file resolves another file's imported type via
	// `sym.Resolved.Type` — without this pre-pass, file order in
	// Sweep C-types determines whether that pointer is set, and a struct
	// field whose annotation is the un-yet-built imported type silently
	// drops out (e.g. `logger: Logger` in runtime.nomi when log.nomi's
	// Pass 1 hadn't run yet). The pre-pass eliminates that order
	// dependency: every file's shells are in place before any file's
	// Pass 2 (resolveTypeExpr) consults them.
	for _, fb := range all {
		BuildTypeShells(fb.fa, fb.nodes)
	}
	// Before BuildTypes, which reads TypeBool and TypeOrdering. See
	// installCanonicalStdTypes.
	installCanonicalStdTypes(cache)

	// Sweep C-types: BuildTypes. BuildTypes returns its diagnostics as a
	// slice (it does not append to fa.TypeErrors itself), so capture
	// them here. Callers that layer CheckTypes / AnalyzeIterSensitivity
	// on top should append to fa.TypeErrors rather than overwrite it.
	for _, fb := range all {
		typeErrs := BuildTypes(fb.fa, fb.nodes)
		fb.fa.TypeErrors = append(fb.fa.TypeErrors, typeErrs...)
	}

	// Receiver identities, now that types exist to read Origin from.
	PopulateQualifiedReceivers(implIndex, filesByKey)
	// Interface identities, same precondition and the same reason: two
	// same-named interfaces are two interfaces. See interface_identity.go.
	PopulateImplIfaceOrigins(implIndex, filesByKey)
	// Inherent (type-owned) receiver identities, same precondition again:
	// without them `detectInherentImplCollisions` groups a local `Date.new`
	// with `std/calendar`'s. See inherent_identity.go.
	PopulateInherentReceiverOrigins(inherentImpls, filesByKey)
	// Conformance identities, same precondition once more: without them
	// `DetectMissingImpls` judges an `Equatable` demand on a user's own
	// `Error` satisfied by std/calendar's `derive Equatable for Error`, and
	// accepts a program with no impl of its own. See missing_impl_identity.go.
	PopulateImplsByIdentity(implIndex, filesByKey)
	for fn, qualified := range implIndex.QualifiedReceiver {
		qualifiedImplBlockReceivers[fn] = qualified
	}

	// Re-aggregate the general ImplTypeArgs interface type-argument templates
	// into the shared project index now that Sweep C-types (BuildTypes, just
	// above) has recorded them. buildProjectImplIndex ran much earlier (before
	// this sweep), so its ImplTypeArgs union captured an empty snapshot — every
	// `impl Iface for T { ... }` template (`recordImplTypeArgsFromBlock`,
	// type_builder.go Pass 3) is populated only here in Sweep C-types. The
	// index's OTHER unions (Impls, ImplManifest, IfaceMethodImpls) are fine:
	// they're recorded in the earlier Sweep B-impls. implIndex is shared by
	// pointer with every fa.ProjectImpls (broadcast above), so mutating this map
	// updates every consumer — notably the checker's CheckTypes pass, which runs
	// after this function returns and uses ImplTypeArgs to bind a generic
	// interface's type params when unifying an interface-typed parameter against
	// a concrete argument (e.g. an `Iter<T>`-typed param against a custom
	// `impl Iter` argument — Set, the std/iter.nomi lazy wrappers). Without this,
	// inline-lambda inference through those HOFs fails ("binary * type mismatch:
	// T vs Int"). Order: precompiled stdlib FAs first, then cache, then `all` —
	// so a freshly-built template wins over a (possibly stale/empty) precompiled
	// one on a name collision.
	if implIndex != nil {
		mergeImplTypeArgs := func(src map[string]map[string]*ImplTypeArgs) {
			for typeName, byIface := range src {
				if implIndex.ImplTypeArgs[typeName] == nil {
					implIndex.ImplTypeArgs[typeName] = make(map[string]*ImplTypeArgs)
				}
				for ifaceName, ita := range byIface {
					implIndex.ImplTypeArgs[typeName][ifaceName] = ita
				}
			}
		}
		mergeImplTypeArgSets := func(src map[string]map[string][]*ImplTypeArgs) {
			for typeName, byIface := range src {
				if implIndex.ImplTypeArgSets[typeName] == nil {
					implIndex.ImplTypeArgSets[typeName] = make(map[string][]*ImplTypeArgs)
				}
				for ifaceName, set := range byIface {
					implIndex.ImplTypeArgSets[typeName][ifaceName] = append(implIndex.ImplTypeArgSets[typeName][ifaceName], set...)
				}
			}
		}
		for _, fa := range stdlibFAs {
			mergeImplTypeArgs(fa.ImplTypeArgs)
			mergeImplTypeArgSets(fa.ImplTypeArgSets)
		}
		for _, fa := range cache {
			mergeImplTypeArgs(fa.ImplTypeArgs)
			mergeImplTypeArgSets(fa.ImplTypeArgSets)
		}
		for _, fb := range all {
			mergeImplTypeArgs(fb.fa.ImplTypeArgs)
			mergeImplTypeArgSets(fb.fa.ImplTypeArgSets)
		}
		// Now that BuildTypes (Sweep C-types, just above) has populated
		// sym.Type on every impl's owning Symbol, fill the impl-FuncType
		// index. Read by the checker's module-qualified dispatch path
		// to recover the actually-resolved impl's return type when a
		// module exports collapse multi-impl methods to one signature.
		PopulateProjectImplFuncTypes(implIndex, filesByKey)
		PopulateProjectTypeMethodSymbols(implIndex, filesByKey)
		// Same precondition, and it must run AFTER the refresh above so it
		// re-keys the post-BuildTypes symbols: `Type.method` resolution needs
		// the receiver's declaring module, or three same-named receivers share
		// one slot and map iteration order picks the winner.
		PopulateTypeMethodIdentities(implIndex, filesByKey)
		PopulateEnumDecls(implIndex, filesByKey)
	}

	// Re-seed ContextType from the cache now that std/context.nomi's
	// BuildTypes has populated its `Context` symbol's Type field.
	// Stdlib flows through discovery so std/context
	// lives in `cache`; lib.Modules from a parallel std.Load() carries
	// a DIFFERENT Context instance with no shared pointer identity.
	// User code's `context: Context` field is resolved via the cache
	// chain (auto-prepended `import std/context.Context` flows
	// through resolveImport→cache), so ContextType must match. Without
	// this re-seed the no-boot synthesized `App` and any cross-file
	// `expected Context, got Context` argument-type comparisons fire
	// pointer-inequality errors despite identical names.
	if reseeded := findContextTypeFromCache(cache); reseeded != nil {
		for _, fb := range all {
			fb.fa.ContextType = reseeded
		}
	}

	// Every function of the project and every entry boot: a top-level
	// `fn boot` in a file that defines `fn main`. The application-field root
	// check (app_fields.go) follows calls across files, and a `tests`
	// group's `boot` line names one of these boots.
	scopedFunctions := map[*ast.FuncDef]*FileAnalysis{}
	bootFunctions := map[*ast.FuncDef]bool{}
	type entryBoot struct {
		fn *ast.FuncDef
		fb *fileBuilder
	}
	var entryBoots []entryBoot
	for _, fb := range all {
		if fn := findBootFunc(fb.nodes); fn != nil {
			if hasTopLevelMain(fb.nodes) {
				bootFunctions[fn] = true
				entryBoots = append(entryBoots, entryBoot{fn: fn, fb: fb})
			} else {
				fb.fa.BootErrors = append(fb.fa.BootErrors, TypeError{
					Line: fn.Line, Col: fn.Col,
					Message: "`boot` belongs in an entry file, one that defines `fn main`; a `tests` group names an entry's boot with `boot entry.boot(startup)`",
				})
			}
		}
		for _, node := range fb.nodes {
			WalkNodes(node, func(n ast.Node) {
				if fn, ok := n.(*ast.FuncDef); ok {
					scopedFunctions[fn] = fb.fa
				}
			})
		}
	}
	var appTypes []*StructType
	for _, eb := range entryBoots {
		if st := validateScopedBootSignature(eb.fb.fa, eb.fn); st != nil {
			appTypes = append(appTypes, st)
		}
	}
	onces := NewOnceTable()
	for _, fb := range all {
		onces.Index(fb.fa, fb.nodes)
	}
	for _, fb := range all {
		fb.fa.Onces = onces
		fb.fa.ScopedFunctionFiles = scopedFunctions
		fb.fa.BootFunctions = bootFunctions
		fb.fa.AppTypes = appTypes
	}

	// Coherence: reject duplicate (interface, type) impls. The dispatch
	// table is keyed (iface.method, typeName) and a second registration is
	// a silent last-wins overwrite, so two impls for one pair would produce
	// load-order-dependent behaviour.
	//
	// The index is CollisionImplIndex, which unions the reachable set with
	// the project index and dedupes BY DECLARATION. Neither input alone is
	// right and both failure modes were measured — see that function's
	// header. `qualifiedImplBlockReceivers` already folds
	// implIndex.QualifiedReceiver (above), and the interface-key map is
	// merged the same way, so every member of the union groups on the same
	// key shape.
	collisionIndex, collisionFiles := CollisionImplIndex(ifaceImpls, ifaceImplFiles, implIndex)
	collisionIfaceKeys := make(map[*ast.FuncDef]string, len(implBlockInterfaceKeys)+len(implIndex.ImplBlockInterfaceKey))
	for fn, key := range implIndex.ImplBlockInterfaceKey {
		collisionIfaceKeys[fn] = key
	}
	for fn, key := range implBlockInterfaceKeys {
		collisionIfaceKeys[fn] = key
	}
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors, detectImplCollisions(collisionIndex, collisionFiles, qualifiedImplBlockReceivers, collisionIfaceKeys, implIndex.ImplIfaceOrigin)...,
	)
	// Inherent type-function name collisions (two type-body `fn m` items
	// for one type after lowering).
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors, detectInherentImplCollisions(inherentImpls)...,
	)
	// Synthesized inherent-block collisions with the same receiver/signature
	// in one file.
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors, detectInherentImplBlockCollisions(inherentBlocks)...,
	)

	// Orphan rule check fires later in this function (declaringModuleIndex
	// + detectOrphanImpls below) — see the cross-module-segments setup
	// and the detectOrphanImpls call site for the orphan-check body.
	//
	// Orphan rule: every impl must declare either its interface or its
	// receiver type in the implementing module. Without that anchor two
	// independent libraries could ship the same (Iface, Type) pair and
	// detectImplCollisions would have nothing to compare them against
	// at any single user's build. Post-stdlib-as-package cutover, stdlib
	// files are in `filesByKey` just like any other module, so
	// declaringModuleIndex picks up stdlib-declared names from the
	// regular files walk — the helper's stdlibInterfaces/stdlibTypes
	// parameters are passed nil (legacy signature, kept for
	// backwards-compat with hand-rolled callers in tests).
	crossModuleSegments := map[string]bool{}
	if proj != nil {
		for shortName := range proj.ModuleIndex {
			crossModuleSegments[shortName] = true
		}
	}
	declModule := declaringModuleIndex(
		filesByKey, currentModuleName,
		nil, nil, crossModuleSegments,
	)
	implModuleOf := func(fn *ast.FuncDef) string {
		return moduleNameFromKey(ifaceImplFiles[fn], currentModuleName, crossModuleSegments)
	}
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors,
		detectOrphanImpls(ifaceImpls, declModule, implModuleOf, implBlockReceivers)...,
	)
	// Inherent-block orphan rule: the receiver type must be declared in the
	// same file as the inherent impl. Type-qualified functions are the type's
	// own API, not extension methods.
	inherentDeclaredLocally := func(module, name string) bool {
		return declaredTypesByFile[module][name]
	}
	entryFB.fa.TypeErrors = append(
		entryFB.fa.TypeErrors,
		detectInherentImplBlockOrphans(inherentBlocks, inherentDeclaredLocally)...,
	)

	// Missing-impl diagnostic does NOT run here — BuildProjectWithCache
	// finishes before CheckTypes, and the recording sites that matter
	// (CallSite, GenericBoundCheck, TypedLiteralSlot,
	// InterfaceTypedParam) only fire during CheckTypes. Running
	// DetectMissingImpls at this point would see only the derive-time
	// recordings (the four CheckTypes-driven kinds wouldn't be in the
	// manifest yet), so the diagnostic would silently not fire for the
	// common cases. The collision and orphan checks above are correctly
	// placed here because they only depend on impl declarations (Sweep
	// B output), not on CheckTypes recordings.
	//
	// Instead, callers that layer CheckTypes + MergeImplManifest on top
	// of BuildProjectWithCache (internal/frontend's Checker.Analyze,
	// stdcompiler, analysis/document.go's LSP path) call
	// analysis.FinalizeCoherence(entryFA) at the end of their pipeline,
	// once the entry FA's ImplManifest holds every recording.

	// Re-merge Impls + ImplManifest across files so the entry FA sees every
	// file's impl registrations (each file's own impls are recorded during
	// buildModule; this catch-up pass folds siblings into the entry FA).
	for _, fb := range all {
		if fb == entryFB {
			continue
		}
		mergeImpls(entryFB.fa, fb.fa)
		mergeImplManifest(entryFB.fa, fb.fa)
	}
	// Fold every stdlib FA's ImplManifest into the entry FA. The per-FA
	// mergeImplManifest chain in Sweep B-impls (above) only folds
	// manifests from FAs the entry transitively imports; the auto-
	// prepended prelude chain reaches a handful of stdlib modules
	// but stops short of others whose `impl` block bodies record
	// conformance pairs the program ultimately needs at runtime (e.g.
	// std/strings's Debug impl calling `Iter.each_while(s, …)` records
	// (Iter, String) on std/strings's FA, but a program that only
	// uses prelude-promoted names never imports std/strings and so
	// never merges that recording in). Without this fold the entry's
	// manifest is missing those pairs.
	// stdlibFAs (lib.Files) already carries every analyzed stdlib FA
	// with its manifest populated by std.Load(); this loop unconditionally
	// folds them all into the entry manifest.
	if entryFB.fa.ImplManifest == nil {
		entryFB.fa.ImplManifest = make(map[string]map[string][]Recording)
	}
	for _, fa := range stdlibFAs {
		mergeImplManifest(entryFB.fa, fa)
	}

	// Build the per-sibling nodes map for callers that need to layer
	// CheckTypes onto each sibling. proj.Files is already keyed by the
	// same "/"-joined module path the cache uses, and the slices were
	// in-place updated by the @derive synthesis pass above so the nodes
	// returned here exactly match what's in each sibling FA's builder.
	siblingNodes := make(map[string][]ast.Node, len(proj.Files))
	for key, nodes := range proj.Files {
		siblingNodes[key] = nodes
	}
	siblingFAs := cache
	if entryModRel != "" {
		siblingFAs = make(map[string]*FileAnalysis, len(cache))
		for key, fa := range cache {
			if key == entryModRel {
				continue
			}
			siblingFAs[key] = fa
		}
	}

	return entryFB.fa, siblingFAs, siblingNodes
}

// inferAppStruct resolves the declared struct result of a boot function.
func inferAppStruct(bootFn *ast.FuncDef, fas []*FileAnalysis) (Type, *Symbol) {
	for _, fa := range fas {
		sym := fa.Definitions[Pos{Line: bootFn.Line, Col: bootFn.Col}]
		if sym == nil || sym.Node != bootFn {
			continue
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok {
			continue
		}
		st := ft.Return
		if !isScopedSchema(st) {
			return nil, nil
		}
		for _, candidate := range fa.References {
			real := candidate
			for real.Resolved != nil {
				real = real.Resolved
			}
			if real.Type != nil && TypesEqual(real.Type, st) && real.Kind == SymbolStruct {
				return st, real
			}
		}
		return st, nil
	}
	return nil, nil
}

// bootSignatureMsg rejects an entry boot that takes anything but no parameter
// or one Startup.
const bootSignatureMsg = "boot must be `fn boot(): App` or `fn boot(startup: Startup): App`, taking no parameter or one Startup and returning the application struct"

// validateScopedBootSignature checks an entry boot's signature: it takes no
// parameter or one `Startup`, which may have a default, and returns a struct
// or an anonymous record with at most one Context-typed field. It answers the
// struct a valid boot returns, which is an application type, or nil.
func validateScopedBootSignature(fa *FileAnalysis, fn *ast.FuncDef) *StructType {
	sym := fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil {
		return nil
	}
	report := func(message string) *StructType {
		fa.BootErrors = append(fa.BootErrors, TypeError{Line: fn.Line, Col: fn.Col, Message: message})
		return nil
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(fn.TypeParams) > 0 || len(ft.Params) > 1 {
		return report(bootSignatureMsg)
	}
	if len(ft.Params) == 1 {
		startup, ok := ft.Params[0].(*StructType)
		if !ok || startup.Name != "Startup" || startup.Origin != "std/startup" {
			return report(bootSignatureMsg)
		}
	}
	if !isScopedSchema(ft.Return) {
		return report("boot must return a struct, the application type")
	}
	contexts := 0
	for _, field := range SchemaFields(ft.Return) {
		if SameScopedType(field.Type, fa.ContextType) {
			contexts++
		}
	}
	if contexts > 1 {
		return report("an application struct may have at most one Context-typed field")
	}
	st, _ := ft.Return.(*StructType)
	return st
}

func findBootFunc(nodes []ast.Node) *ast.FuncDef {
	for _, n := range nodes {
		fn, ok := n.(*ast.FuncDef)
		if ok && fn.Name == "boot" {
			return fn
		}
	}
	return nil
}

func hasTopLevelMain(nodes []ast.Node) bool {
	for _, n := range nodes {
		fn, ok := n.(*ast.FuncDef)
		if ok && fn.Name == "main" {
			return true
		}
	}
	return false
}

func lookupSymbolDeep(scope *Scope, name string) *Symbol {
	return lookupSymbolDeepSeen(scope, name, make(map[*Scope]bool))
}

func lookupSymbolDeepSeen(scope *Scope, name string, seen map[*Scope]bool) *Symbol {
	if scope == nil || name == "" {
		return nil
	}
	if seen[scope] {
		return nil
	}
	seen[scope] = true
	if sym := scope.Lookup(name); sym != nil {
		return sym
	}
	for _, sym := range scope.Symbols {
		real := sym
		for real != nil && real.Resolved != nil {
			real = real.Resolved
		}
		if real == nil || real.Kind != SymbolModule || real.ModuleScope == nil {
			continue
		}
		if found := lookupSymbolDeepSeen(real.ModuleScope, name, seen); found != nil {
			return found
		}
	}
	return nil
}

func lastSegment(modKey string) string {
	if i := strings.LastIndex(modKey, "/"); i >= 0 {
		return modKey[i+1:]
	}
	return modKey
}

// IsStdlibKey reports whether the given FileAnalysis cache key (or
// module path) names a stdlib file (first "/" segment == "std"). Used
// by the auto-prepend pass to skip stdlib files (which import their
// dependencies explicitly), by the impl-union/manifest paths to avoid
// double-counting stdlib impls that flow through the regular project
// pipeline, and by the runtime extern validator to skip stdlib externs
// (whose Go-side implementations live in the language runtime, not in
// the embedder's RegisterExternFunc map). Exported so the runtime
// package and any other consumer that classifies module keys uses the
// same single source of truth — historically the check was duplicated
// as `strings.HasPrefix(modulePath, "std/")` in extern_validate.go,
// which doesn't match a bare `"std"` (single-segment) key the way
// this helper does.
func IsStdlibKey(modKey string) bool {
	if modKey == "" {
		return false
	}
	if i := strings.IndexByte(modKey, '/'); i >= 0 {
		return modKey[:i] == "std"
	}
	return modKey == "std"
}

// QualifyIntraStdlibImport rewrites a bare import written INSIDE a stdlib
// source file onto the `std/<name>` form every module key in the toolchain
// uses. `importerKey` is the importing file's own module key.
//
// The stdlib is one Nomi module, so `std/lists.nomi` names its siblings bare
// — `import iter.Iter` — exactly as a user module's files name each other.
// Every consumer of a module key, on the other hand, spells the stdlib
// `std/<name>`: IsStdlibKey, originForKey, resolveStdlibImport, the
// `filesByKey` the orphan rule and impl coherence read, the `Origin` on every
// declaration, and the runtime's dispatch qualifier. Prefixing here is what
// makes ONE file ONE module key regardless of which spelling reached it.
//
// Leaving the bare form alone instead is not an option: `std/lists`'s `iter`
// and an outside `import std/iter` would then be two keys for one file, and
// every type it declares would have two declarations. That is the same defect
// the nested-facade layout produced at the runtime's key derivation, where it
// cost 26 attached-test failures reading "expected Date, got Date — same name,
// different declarations".
func QualifyIntraStdlibImport(importerKey string, modPath []string) []string {
	if len(modPath) == 0 || modPath[0] == "std" || !IsStdlibKey(importerKey) {
		return modPath
	}
	out := make([]string, 0, len(modPath)+1)
	out = append(out, "std")
	return append(out, modPath...)
}

// findContextTypeFromCache returns the Type of `Context` exported by
// std/context.nomi as discovered through the project's cache, or nil
// when the cache doesn't include `std/context`. Mirror of the
// `modules`-keyed findContextType but routed through the post-cutover
// cache so the Context identity matches what user files resolve via
// their auto-prepended `import std/context.Context` (or via
// import-side propagation through other modules).
func findContextTypeFromCache(cache map[string]*FileAnalysis) Type {
	ctxFA, ok := cache["std/context"]
	if !ok || ctxFA == nil {
		return nil
	}
	sym := ctxFA.ModuleScope.Lookup("Context")
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil && sym.Resolved.Type != nil {
		return sym.Resolved.Type
	}
	return sym.Type
}
