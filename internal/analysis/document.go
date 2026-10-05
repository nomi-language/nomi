package analysis

import (
	"context"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Document is an editor-open file with its latest text and analysis.
//
// Content and Version are the latest text. Nodes, Errors, Damaged and
// Analysis come from the last finished analysis, which was built from
// AnalyzedContent at AnalyzedVersion; while an edit waits for analysis the
// two differ. Every field is guarded by DocumentManager.mu: read them
// through Snapshot from any goroutine that may run alongside analysis.
type Document struct {
	URI     string
	Content string
	// Version counts content changes. It is the manager's own counter, not
	// the client's document version.
	Version int
	Nodes   []ast.Node
	// AnalyzedContent and AnalyzedVersion are the text and version the
	// installed Nodes, Errors, Damaged and Analysis were built from.
	// AnalyzedVersion is 0 until the first analysis finishes.
	AnalyzedContent string
	AnalyzedVersion int
	Analysis        *FileAnalysis
	Errors          []parser.ParseError
	// Damaged holds one span per top-level declaration the resilient parse
	// had to repair (empty for a file that parses cleanly). Nodes inside
	// these spans are PARTIAL: some of the declaration was never read, so
	// they carry scopes and symbols but support no judgement about the
	// declaration as a whole. analyze() drops type diagnostics inside them
	// for exactly that reason; see suppressInDamagedSpans.
	Damaged []parser.Span
}

// DocumentManager tracks the open documents, analyzed, and the other
// workspace files, indexed (workspace.go).
//
// Locking discipline:
//   - dm.mu (RWMutex) protects the maps (docs, index, imports, reverseDeps,
//     modules, primitives, workspaceRoot, ffiExternsByRoot), the closed-file
//     cache, and every field of every *Document. It is held only for short
//     critical sections: a map access, a content change, or installing a
//     finished analysis.
//   - dm.analyzeMu (Mutex) serializes builds. BuildProject mutates state
//     shared across documents (notably the primitives scope's Children
//     slice: every NewScope call appends to it), so two builds, even of
//     different documents, would race. A build reads its input text under
//     dm.mu, builds into locals with dm.mu released, and installs the
//     result under dm.mu. Readers therefore never wait for a build: a
//     Snapshot taken while one runs returns the previous analysis.
//
// Lock ordering: analyzeMu first (the outer lock), dm.mu second (inner).
// Nothing that holds dm.mu acquires analyzeMu.
//
// An installed *FileAnalysis and its Nodes are never mutated after
// installation, so a snapshot's pointers stay readable after the next
// analysis replaces them.
type DocumentManager struct {
	mu            sync.RWMutex
	analyzeMu     sync.Mutex
	docs          map[string]*Document
	primitives    *Scope
	modules       map[string]*Scope
	stdlibFAs     map[string]*FileAnalysis // mirrors std.StdLib.Files; see SetStdlib doc.
	workspaceRoot string
	// index holds the workspace files' index entries. An open document's
	// entry is the one from before it was opened, and is replaced when it
	// closes.
	index map[string]*IndexedFile
	// imports maps a file URI to the URIs it imports, from an open
	// document's analyzed nodes or a closed file's index entry, and
	// reverseDeps is its inverse.
	imports          map[string][]string
	reverseDeps      map[string][]string
	ffiExternsByRoot map[string][]syntheticextern.Decl
	// closed caches analyses of closed files built for requests.
	closed closedCache
	// foreground counts the builds that edits and requests are waiting
	// for or running; background builds wait for it to reach zero.
	foreground atomic.Int32
	// installed is closed, and replaced, each time an analysis is
	// installed on any document. See Installed.
	installed chan struct{}
}

// DocSnapshot is a frozen view of a Document, taken under the manager's
// lock. Pointer fields (Analysis, Nodes) reference the same objects the
// live Document holds; they are not deep-copied, and they are never
// mutated after installation.
//
// Content is the text Nodes and Analysis were built from, so every
// position in them agrees with Content. Text is the latest text, which is
// newer than Content while an edit waits for analysis. A handler that
// reads the cursor's surroundings from the text (completion, signature
// help) or edits the text as a whole (formatting) reads Text; anything
// that turns analysis positions into ranges reads Content.
type DocSnapshot struct {
	URI      string
	Open     bool
	Content  string
	Nodes    []ast.Node
	Errors   []parser.ParseError
	Damaged  []parser.Span
	Analysis *FileAnalysis
	// Text and Version are the latest text and its version.
	// AnalyzedVersion is the version Content and Analysis belong to.
	Text            string
	Version         int
	AnalyzedVersion int
}

// Current reports whether the snapshot's analysis was built from its
// latest text.
func (s *DocSnapshot) Current() bool {
	return s.AnalyzedVersion == s.Version
}

func NewDocumentManager() *DocumentManager {
	return &DocumentManager{
		docs:             make(map[string]*Document),
		index:            make(map[string]*IndexedFile),
		imports:          make(map[string][]string),
		reverseDeps:      make(map[string][]string),
		ffiExternsByRoot: make(map[string][]syntheticextern.Decl),
		installed:        make(chan struct{}),
	}
}

// SetStdlib configures the primitives, module scopes, and per-stdlib-
// module FileAnalysis map from the std. The stdlibFAs map (typically
// std.StdLib.Files) is threaded into BuildProject(WithCache) so the
// resolver can write stdlib FAs through to b.cache on first reference.
// Pass `nil` if the caller
// has no stdlib (same situations that pass `nil` for `modules`).
func (dm *DocumentManager) SetStdlib(primitives *Scope, modules map[string]*Scope, stdlibFAs map[string]*FileAnalysis) {
	dm.mu.Lock()
	dm.primitives = primitives
	dm.modules = modules
	dm.stdlibFAs = stdlibFAs
	dm.mu.Unlock()
}

// Open registers a document as editor-open and analyzes it before
// returning.
func (dm *DocumentManager) Open(uri, content string) *Document {
	dm.SetText(uri, content)
	doc := dm.Get(uri)
	dm.analyze(doc)
	return doc
}

// Update replaces content and analyzes it before returning.
func (dm *DocumentManager) Update(uri, content string) *Document {
	return dm.Open(uri, content)
}

// SetText records new editor text for uri, opening it if it is not open,
// and returns the text's version. It runs no analysis; AnalyzeLatest does.
func (dm *DocumentManager) SetText(uri, content string) int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	doc, exists := dm.docs[uri]
	if !exists {
		doc = &Document{URI: uri}
		dm.docs[uri] = doc
		dm.closed.remove(uri)
	}
	doc.Content = content
	doc.Version++
	return doc.Version
}

// IsOpen reports whether uri is an open document.
func (dm *DocumentManager) IsOpen(uri string) bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	_, ok := dm.docs[uri]
	return ok
}

// AnalyzeLatest analyzes uri's latest text unless that text is already
// analyzed. It returns the version it analyzed and true, or false when
// there was nothing to do: the document is untracked, already current, or
// ctx was cancelled before the build started. A build that started runs to
// completion and is installed even if ctx is cancelled meanwhile, because
// it is newer than the analysis it replaces.
func (dm *DocumentManager) AnalyzeLatest(ctx context.Context, uri string) (int, bool) {
	dm.mu.RLock()
	doc, ok := dm.docs[uri]
	dm.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return dm.analyzeIfStale(ctx, doc)
}

// Installed returns a channel that is closed the next time an analysis is
// installed on any document. Take it before checking a document's
// versions, then wait on it, so no installation is missed.
func (dm *DocumentManager) Installed() <-chan struct{} {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.installed
}

// Close drops an open document and its analysis. A file the workspace scan
// covers (Covers) that exists on disk becomes a closed workspace file
// again, indexed from its disk text; Close reports whether it did.
func (dm *DocumentManager) Close(uri string) bool {
	dm.mu.Lock()
	_, ok := dm.docs[uri]
	delete(dm.docs, uri)
	dm.mu.Unlock()
	if !ok {
		return false
	}
	if dm.Covers(uri) && dm.IndexFile(uri) {
		return true
	}
	dm.RemoveWorkspaceFile(uri)
	return false
}

// Get returns the open document for the given URI, or nil.
//
// Get synchronises only the map lookup. Read the document's fields through
// Snapshot from any goroutine that may run alongside analysis.
func (dm *DocumentManager) Get(uri string) *Document {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.docs[uri]
}

// Snapshot returns a frozen view of the open document. It never waits for
// a running analysis: the view carries the last installed one. Returns nil
// if the URI is not open; Analyzed also answers for a closed file.
func (dm *DocumentManager) Snapshot(uri string) *DocSnapshot {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	doc, ok := dm.docs[uri]
	if !ok {
		return nil
	}
	return &DocSnapshot{
		URI:             doc.URI,
		Open:            true,
		Content:         doc.AnalyzedContent,
		Nodes:           doc.Nodes,
		Errors:          doc.Errors,
		Damaged:         doc.Damaged,
		Analysis:        doc.Analysis,
		Text:            doc.Content,
		Version:         doc.Version,
		AnalyzedVersion: doc.AnalyzedVersion,
	}
}

// projectRootFromURI returns the project root for a file:// URI by
// walking upward from the file looking for a project marker. See
// FindProjectRoot for the rule. Falls back to the file's own directory.
func (dm *DocumentManager) projectRootFromURI(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	return dm.FindProjectRoot(path)
}

// FindProjectRoot is ProjectRoot for filePath, bounded by the editor's
// workspace root when the client supplied one.
func (dm *DocumentManager) FindProjectRoot(filePath string) string {
	dm.mu.RLock()
	workspaceRoot := dm.workspaceRoot
	dm.mu.RUnlock()
	return ProjectRoot(filePath, workspaceRoot)
}

// ProjectRoot is the directory a file's imports resolve from (spec,
// "Project root discovery"), walking upward from the file:
//
//  1. The first directory containing nomi.toml: project mode.
//  2. Else the first directory containing main.nomi: file mode.
//  3. Else the file's own directory: a single-file script.
//
// A non-empty bound stops each walk there; it never goes above it. The CLI
// passes none and the LSP passes the editor's workspace root. Both use this
// one function, so a file opened in the editor resolves its imports against
// the same directory `nomi run` would.
func ProjectRoot(filePath, bound string) string {
	fileDir := filepath.Dir(filePath)
	for _, marker := range []string{"nomi.toml", "main.nomi"} {
		if dir, ok := nearestAncestorWith(fileDir, marker, bound); ok {
			return dir
		}
	}
	return fileDir
}

// nearestAncestorWith is the nearest directory from dir upward, stopping at
// bound when it is non-empty, that holds a file named name.
func nearestAncestorWith(dir, name, bound string) (string, bool) {
	for {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return dir, true
		}
		if dir == bound {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// makeLoader creates a FileLoader that resolves modules from open documents or disk.
func (dm *DocumentManager) makeLoader() FileLoader {
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		decls := dm.syntheticExternsForRoot(projectRoot)

		// A tracked file is parsed afresh from its text, never handed out
		// as its installed nodes: the build writes into the nodes it is
		// given (the builder records an impl item's interface, the checker
		// a dot variant's enum, and CheckImplImports type-checks every
		// sibling), and request handlers read installed nodes without the
		// build lock. The text is the one its installed analysis was built
		// from, so the two agree; before its first analysis, its latest.
		uri := "file://" + filePath
		dm.mu.RLock()
		doc, ok := dm.docs[uri]
		var text string
		if ok {
			text = doc.AnalyzedContent
			if doc.AnalyzedVersion == 0 {
				text = doc.Content
			}
		}
		dm.mu.RUnlock()
		if ok {
			nodes, _, _ := parser.ParseResilient(lexer.Lex(text))
			return syntheticextern.Inject(nodes, decls, false)
		}

		// Read from disk, lex, and parse.
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return syntheticextern.Inject(nodes, decls, false)
	}
}

func (dm *DocumentManager) syntheticExternsForRoot(root string) []syntheticextern.Decl {
	if root == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil
	}
	dm.mu.RLock()
	decls, ok := dm.ffiExternsByRoot[root]
	dm.mu.RUnlock()
	if ok {
		return decls
	}
	pkgs, err := ffirun.Discover(root)
	if err != nil {
		return nil
	}
	var out []syntheticextern.Decl
	for _, pkg := range pkgs {
		for _, typ := range pkg.Types {
			if typ.Declaration != "" {
				out = append(out, syntheticextern.Decl{
					Key:        typ.Key,
					Source:     typ.Declaration,
					SourceFile: typ.SourceFile,
					SourceLine: typ.SourceLine,
					SourceCol:  typ.SourceCol,
					SourceSpan: len(typ.TypeName),
					OwnerFile:  pkg.OwnerFile,
					OwnerLine:  pkg.OwnerLine,
					OwnerCol:   pkg.OwnerCol,
					OwnerSpan:  len(pkg.Owner),
				})
			}
		}
		for _, exp := range pkg.Exports {
			if exp.Declaration != "" {
				out = append(out, syntheticextern.Decl{
					Key:        exp.Key,
					Source:     exp.Declaration,
					SourceFile: exp.SourceFile,
					SourceLine: exp.SourceLine,
					SourceCol:  exp.SourceCol,
					SourceSpan: len(exp.FuncName),
					OwnerFile:  pkg.OwnerFile,
					OwnerLine:  pkg.OwnerLine,
					OwnerCol:   pkg.OwnerCol,
					OwnerSpan:  len(pkg.Owner),
				})
			}
		}
	}
	dm.mu.Lock()
	dm.ffiExternsByRoot[root] = out
	dm.mu.Unlock()
	return out
}

func isFFIEntryPath(path string) bool {
	base := filepath.Base(path)
	return base == "main.nomi" || strings.HasSuffix(base, "_test.nomi")
}

// isStdlibFile returns true when the document's URI points at a stdlib
// .nomi file — either the cache-materialized copy FileURI hands out for
// jump-to-def, the in-repo source at std/, or a co-located
// adapter's facade at std/<name>/<name>.nomi. The question is
// stdlibModuleForPath's; see there for why it is not a look at one directory.
//
// TODO: "this file is the canonical source for a prelude name" is
// currently encoded in three places that have to agree: the production
// stdlib loader (`std.Load` passes nil primitives), this LSP
// detector (path heuristic), and the reserved-name check in
// `builder.checkReservedTypeName` (purely scope-mechanical, blind to
// file identity). The heuristic holds for today's layout, but two
// cleaner shapes exist if it ever drifts:
//
//   - Make `checkReservedTypeName` identity-aware: if the redeclaring
//     symbol's source position matches the prelude's `Resolved` source
//     for the same name, suppress the error. Then no caller needs to
//     know about "stdlib-ness."
//   - Or tag stdlib-ness on `Document` (or pass it through as a flag
//     on the build call) so every caller consults one source of truth.
//
// Neither is urgent; revisit if a third entry point grows or if the
// stdlib filesystem layout changes.
func (dm *DocumentManager) isStdlibFile(uri string) bool {
	_, ok := stdlibModuleForPath(strings.TrimPrefix(uri, "file://"))
	return ok
}

// SetWorkspaceRoot sets the root directory for workspace scanning.
func (dm *DocumentManager) SetWorkspaceRoot(root string) {
	dm.mu.Lock()
	dm.workspaceRoot = root
	dm.mu.Unlock()
}

// WorkspaceRoot returns the workspace root directory.
func (dm *DocumentManager) WorkspaceRoot() string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.workspaceRoot
}

// ReverseDeps returns the URIs of files that import the given URI.
func (dm *DocumentManager) ReverseDeps(uri string) []string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return append([]string(nil), dm.reverseDeps[uri]...)
}

// UpdateImportEdges replaces the open document's import edges with those of
// its installed nodes, and drops the cached closed-file analyses its new
// text makes stale. Installed nodes are never mutated, so the walk runs
// without a lock.
func (dm *DocumentManager) UpdateImportEdges(uri string) {
	dm.mu.RLock()
	doc, ok := dm.docs[uri]
	var nodes []ast.Node
	if ok {
		nodes = doc.Nodes
	}
	dm.mu.RUnlock()
	if !ok {
		return
	}
	imports := ExtractImportsFromNodes(nodes, dm.projectRootFromURI(uri))
	dm.mu.Lock()
	if _, open := dm.docs[uri]; open {
		dm.setImportsLocked(uri, imports)
	}
	dm.mu.Unlock()
	dm.invalidateClosed(uri)
}

// PropagateChange carries a change to uri to the files that import it,
// transitively. It re-analyzes the open ones and returns them, and returns
// the closed ones' URIs, whose cached analyses it has dropped.
func (dm *DocumentManager) PropagateChange(uri string) (open []*Document, closed []string) {
	dm.invalidateClosed(uri)
	visited := map[string]bool{uri: true}
	queue := dm.ReverseDeps(uri)
	for len(queue) > 0 {
		depURI := queue[0]
		queue = queue[1:]
		if visited[depURI] {
			continue
		}
		visited[depURI] = true
		if doc := dm.Get(depURI); doc != nil {
			dm.analyze(doc)
			dm.UpdateImportEdges(depURI)
			open = append(open, doc)
		} else {
			closed = append(closed, depURI)
		}
		for _, transitive := range dm.ReverseDeps(depURI) {
			if !visited[transitive] {
				queue = append(queue, transitive)
			}
		}
	}
	return open, closed
}

// ReanalyzeOpen re-analyzes every open document except excludeURI and
// returns them. A new file can resolve an import any of them names.
func (dm *DocumentManager) ReanalyzeOpen(excludeURI string) []*Document {
	dm.mu.RLock()
	var docs []*Document
	for uri, doc := range dm.docs {
		if uri != excludeURI {
			docs = append(docs, doc)
		}
	}
	dm.mu.RUnlock()
	for _, doc := range docs {
		dm.analyze(doc)
		dm.UpdateImportEdges(doc.URI)
	}
	return docs
}

// InvalidateFFIExterns clears cached tag-derived FFI declarations and the
// cached closed-file analyses, and re-analyzes every open document. It
// returns the open documents and the URIs of the closed files whose project
// root holds a go.mod, the only ones whose analysis reads those
// declarations. This invalidates broadly: a changed Go file can belong to a
// replace-target binding whose declarations a different project consumes.
func (dm *DocumentManager) InvalidateFFIExterns() (open []*Document, closed []string) {
	dm.mu.Lock()
	dm.ffiExternsByRoot = make(map[string][]syntheticextern.Decl)
	dm.closed.clear()
	for _, doc := range dm.docs {
		open = append(open, doc)
	}
	for uri, entry := range dm.index {
		if _, isOpen := dm.docs[uri]; !isOpen {
			if _, err := os.Stat(filepath.Join(entry.Root, "go.mod")); err == nil {
				closed = append(closed, uri)
			}
		}
	}
	dm.mu.Unlock()
	sort.Strings(closed)
	for _, doc := range open {
		dm.analyze(doc)
		dm.UpdateImportEdges(doc.URI)
	}
	return open, closed
}

// builtAnalysis is one finished build, not yet installed.
type builtAnalysis struct {
	nodes   []ast.Node
	errs    []parser.ParseError
	damaged []parser.Span
	fa      *FileAnalysis
}

// analyze builds the document's latest text and installs the result.
func (dm *DocumentManager) analyze(doc *Document) {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	dm.analyzeLocked(doc)
}

// analyzeIfStale is analyze for a document whose latest text may already
// be analyzed: it builds only when it is not, and only when ctx is still
// live once the build lock is held. See AnalyzeLatest.
func (dm *DocumentManager) analyzeIfStale(ctx context.Context, doc *Document) (int, bool) {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	if ctx.Err() != nil {
		return 0, false
	}
	dm.mu.RLock()
	current := doc.AnalyzedVersion == doc.Version
	dm.mu.RUnlock()
	if current {
		return 0, false
	}
	return dm.analyzeLocked(doc), true
}

// analyzeLocked builds the document's latest text and installs the
// result unless a newer version is already installed. The caller holds
// analyzeMu; dm.mu is taken only to read the text and to install. It
// returns the version it built.
func (dm *DocumentManager) analyzeLocked(doc *Document) int {
	dm.mu.RLock()
	uri, content, version := doc.URI, doc.Content, doc.Version
	dm.mu.RUnlock()

	b := dm.build(uri, content)

	dm.mu.Lock()
	if version >= doc.AnalyzedVersion {
		doc.Nodes = b.nodes
		doc.Errors = b.errs
		doc.Damaged = b.damaged
		doc.Analysis = b.fa
		doc.AnalyzedContent = content
		doc.AnalyzedVersion = version
	}
	close(dm.installed)
	dm.installed = make(chan struct{})
	dm.mu.Unlock()
	return version
}

// build lexes, parses and analyzes one text of the document at uri. It
// touches no Document; analyzeLocked installs what it returns.
func (dm *DocumentManager) build(uri, content string) builtAnalysis {
	tokens := lexer.Lex(content)
	// ParseResilient, not ParseWithRecovery: a syntax error inside a
	// function body must not discard the whole function, or completion at
	// the cursor has no scopes to offer. `errs` is identical to what
	// ParseWithRecovery reports — recovery adds nodes, never diagnostics —
	// and `damaged` names the declarations that were repaired.
	nodes, errs, damaged := parser.ParseResilient(tokens)
	root := dm.projectRootFromURI(uri)
	docPath := strings.TrimPrefix(uri, "file://")
	if injected, err := syntheticextern.Inject(nodes, dm.syntheticExternsForRoot(root), isFFIEntryPath(docPath)); err == nil {
		nodes = injected
	}
	// Type-body lowering on the doc's own slice, BEFORE any build below.
	// The build pipelines lower internally, but their extended slices stay
	// internal — the CheckTypes / AnalyzeIterSensitivity / MarkTailCalls
	// calls in every branch of this function run over THIS slice, so derived
	// impl blocks must be present here for focused-doc analysis.
	//
	// Lowering reports genuine user-facing errors — a duplicate conformance, a
	// derive of an unknown protocol, or a derive colliding with a hand-written
	// impl method. They must reach the editor as diagnostics (parity with
	// `nomi run`). Lowering CONSUMES each decl's Items, so the subsequent
	// build's internal re-lowering over this same slice finds nothing to
	// re-report — this call is the only place these errors exist, so we
	// capture them here and fold them into the focused doc's TypeErrors once
	// the build has created fa (below, after every branch).
	nodes, lowerErrs := LowerDerives(nodes)
	testNameErrs := CheckDuplicateTestNames(nodes)
	fa := dm.buildAnalysis(uri, nodes, root, docPath)
	if fa != nil {
		if len(lowerErrs) > 0 {
			fa.TypeErrors = append(fa.TypeErrors, lowerErrs...)
		}
		if len(testNameErrs) > 0 {
			fa.TypeErrors = append(fa.TypeErrors, testNameErrs...)
		}
		// Last, after every branch and the two folds above: a declaration
		// the parser had to repair is only partly read, so no type claim
		// about it is meaningful. Reporting those claims would bury the
		// real syntax error under invented type errors on the valid half.
		fa.TypeErrors = suppressInDamagedSpans(fa.TypeErrors, damaged)
	}
	return builtAnalysis{nodes: nodes, errs: errs, damaged: damaged, fa: fa}
}

// buildAnalysis builds and checks the analysis of one document's nodes:
// the stdlib branch, a project built from an entry that imports the
// document, or the document as its own entry. Every branch builds with a
// loader that answers the document's own module path with nodes, so the
// build never reads the document's previously installed nodes.
func (dm *DocumentManager) buildAnalysis(uri string, nodes []ast.Node, root, docPath string) (fa *FileAnalysis) {
	// Stdlib files (~/.cache/nomi/stdlib/<mod>.nomi for jump-to-def
	// materialization, stdlib/<mod>.nomi for direct repo
	// opens) must not receive the prelude — they import their
	// dependencies by hand, and getting the prelude here would trip
	// the reserved-name check on every stdlib type the prelude itself
	// re-exports (Maybe / Result / Display / Debug / ...). Detected
	// by an immediate-parent directory named "stdlib"; that's both
	// what FileURI produces and how repo trees are laid out.
	if dm.isStdlibFile(uri) {
		fa = BuildFileWithStdlibAtPath(nodes, nil, dm.modules, root, dm.makeLoaderWithOverride(uri, nodes), docPath)
		// Attach the cached stdlib ProjectImpls index so the checker's
		// dispatch-return-type resolution (via `ImplFuncTypes`) sees the
		// other stdlib impls when type-checking this stdlib file.
		// Without this, a bare `add(value, ...)` inside std/calendar's
		// `impl Add<Weeks, DateTime> for DateTime` function can pick an
		// arbitrary `add` impl's signature and surface a
		// spurious "expected OffsetDateTime, got DateTime" return-type
		// mismatch (the runtime dispatches correctly; the LSP-only
		// analyzer pass is the only place this surfaces).
		AttachStdlibProjectImpls(fa, dm.stdlibFAs)
		// BuildFileWithStdlib stops before Sweep C (BuildTypes); the project
		// branches below get Sweep C for free via BuildProjectFromEntry. Run
		// it here too, or *inferred* types never resolve for this file — e.g. a
		// distinct-destructure binding (`Duration(ns) = d` → `ns: Int`) stays
		// untyped because the checker can't see the distinct type's Inner,
		// and hover shows a bare name instead of `ns: Int`.
		typeErrs := BuildTypes(fa, nodes)
		checkErrs := CheckTypes(fa, nodes)
		iterErrs := AnalyzeIterSensitivity(fa, nodes)
		MarkTailCalls(nodes)
		fa.TypeErrors = append(fa.TypeErrors, typeErrs...)
		fa.TypeErrors = append(fa.TypeErrors, checkErrs...)
		fa.TypeErrors = append(fa.TypeErrors, iterErrs...)
		// Concurrency layer 1 structural rules.
		fa.TypeErrors = append(fa.TypeErrors, CheckConcurrentScope(fa, nodes)...)
		fa.TypeErrors = append(fa.TypeErrors, CheckBootScope(fa, nodes)...)
		fa.TypeErrors = append(fa.TypeErrors, CheckTaskLifetime(fa, nodes)...)
		// FinalizeCoherence: now exercises the attached ProjectImpls
		// (above), so missing-impl diagnostics that recordings inside a
		// stdlib file demand surface here. Listed for parity with the
		// project branches below.
		fa.TypeErrors = append(fa.TypeErrors, FinalizeCoherence(fa)...)
		return
	}

	// Prefer building the project from an entry file when the open doc is
	// a file an entry imports rather than an entry itself: main.nomi, then
	// each of nomi.toml's entry_points. DiscoverProject walks imports
	// forward only, so a file that an entry imports but that does not
	// import the entry (e.g. greeter.nomi when main.nomi imports it) would
	// otherwise never see the entry's boot, and so not know the
	// application type whose fields it reads.
	for _, mainPath := range dm.entryCandidates(root) {
		if mainPath == docPath {
			continue
		}
		if mainContent, err := os.ReadFile(mainPath); err == nil {
			mainTokens := lexer.Lex(string(mainContent))
			mainNodes, _ := parser.ParseWithRecovery(mainTokens)
			loader := dm.makeLoaderWithOverride(uri, nodes)
			entryFA, cache, siblingNodes := BuildProjectFromEntry(mainPath, mainNodes, dm.primitives, dm.modules, dm.stdlibFAs, root, loader)
			docKey := relModuleKey(root, docPath)
			if cached, ok := cache[docKey]; ok && cached != nil {
				fa = cached
				checkErrs := CheckTypes(fa, nodes)
				iterErrs := AnalyzeIterSensitivity(fa, nodes)
				// MarkTailCalls mutates the AST in place, setting
				// Call.IsTailCall on every call in tail position, which the
				// IR carries as ir.Call.Tail. Must run
				// on the same `nodes` slice the doc holds.
				MarkTailCalls(nodes)
				fa.TypeErrors = append(fa.TypeErrors, checkErrs...)
				fa.TypeErrors = append(fa.TypeErrors, iterErrs...)
				// Concurrency layer 1 structural rules.
				fa.TypeErrors = append(fa.TypeErrors, CheckConcurrentScope(fa, nodes)...)
				fa.TypeErrors = append(fa.TypeErrors, CheckBootScope(fa, nodes)...)
				fa.TypeErrors = append(fa.TypeErrors, CheckTaskLifetime(fa, nodes)...)
				// Post-CheckTypes coherence: missing-impl diagnostic et
				// al. Must come AFTER CheckTypes — the LSP runs
				// CheckTypes only on the focused doc here (no
				// per-sibling fold), so the manifest carries the
				// focused doc's recordings. Diagnostics for missing
				// impls demanded only by sibling files won't surface
				// until those siblings are opened.
				fa.TypeErrors = append(fa.TypeErrors, FinalizeCoherence(fa)...)
				fa.TypeErrors = append(fa.TypeErrors, checkDocImplImports(fa, nodes, entryFA, mainNodes, cache, siblingNodes)...)
				return
			}
		}
	}

	// Fallback: analyze the open doc as the entry. Single-file scripts,
	// main.nomi itself, or any file outside a project with main.nomi at
	// its root take this path.
	//
	// Use BuildProjectFromEntry so the entry's FilePath is populated
	// from docPath. Without this, internal/-access checks
	// compute the importer mod-rel from an empty FilePath and
	// falsely reject `import foo/internal/x` from any non-main entry —
	// a secondary entry like tools/seed.nomi opened in the LSP would
	// see "outside the parent subtree of foo/internal/" even when the
	// access is valid.
	//
	// BuildProjectFromEntry runs Sweep C (BuildTypes) internally and
	// populates fa.TypeErrors with its results. CheckTypes
	// and AnalyzeIterSensitivity layer additional diagnostics on top.
	fallbackDocPath := strings.TrimPrefix(uri, "file://")
	entryModRel := relModuleKey(root, fallbackDocPath)
	docEntryFA, docSiblings, docSiblingNodes := BuildProjectFromEntryWithManifest(fallbackDocPath, nodes, dm.primitives, dm.modules, dm.stdlibFAs, root, dm.makeLoaderWithOverride(uri, nodes), nil, entryModRel)
	fa = docEntryFA
	checkErrs := CheckTypes(fa, nodes)
	iterErrs := AnalyzeIterSensitivity(fa, nodes)
	// MarkTailCalls mutates the AST in place, setting Call.IsTailCall on
	// every call in tail position, which the IR carries as ir.Call.Tail.
	// Must run on the same `nodes` slice the doc holds.
	MarkTailCalls(nodes)
	fa.TypeErrors = append(fa.TypeErrors, checkErrs...)
	fa.TypeErrors = append(fa.TypeErrors, iterErrs...)
	// Concurrency layer 1 structural rules.
	fa.TypeErrors = append(fa.TypeErrors, CheckConcurrentScope(fa, nodes)...)
	fa.TypeErrors = append(fa.TypeErrors, CheckBootScope(fa, nodes)...)
	fa.TypeErrors = append(fa.TypeErrors, CheckTaskLifetime(fa, nodes)...)
	// Post-CheckTypes coherence: missing-impl diagnostic et al. See
	// internal/frontend's Checker.Analyze for the rationale; here the focused doc IS the
	// entry FA, so its post-CheckTypes ImplManifest is what
	// FinalizeCoherence reads.
	fa.TypeErrors = append(fa.TypeErrors, FinalizeCoherence(fa)...)
	fa.TypeErrors = append(fa.TypeErrors, checkDocImplImports(fa, nodes, docEntryFA, nodes, docSiblings, docSiblingNodes)...)
	return fa
}

// checkDocImplImports settles the open document's ImplImports (see
// CheckImplImports) and returns the errors for the ones its program does not
// need. Settling reads every project file's uses of impl blocks, so it
// type-checks the project's other files, which the document's analysis
// otherwise skips. It does that only when the document has such an import.
func checkDocImplImports(docFA *FileAnalysis, docNodes []ast.Node, entryFA *FileAnalysis, entryNodes []ast.Node, files map[string]*FileAnalysis, fileNodes map[string][]ast.Node) []TypeError {
	if docFA == nil || len(docFA.ImplImports) == 0 {
		return nil
	}
	program := []ProgramFile{{FA: docFA, Nodes: docNodes}}
	seen := map[*FileAnalysis]bool{docFA: true}
	if entryFA != nil && !seen[entryFA] {
		CheckTypes(entryFA, entryNodes)
		program = append(program, ProgramFile{FA: entryFA, Nodes: entryNodes})
		seen[entryFA] = true
	}
	keys := make([]string, 0, len(fileNodes))
	for key := range fileNodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fa := files[key]
		if fa == nil || seen[fa] {
			continue
		}
		CheckTypes(fa, fileNodes[key])
		program = append(program, ProgramFile{FA: fa, Nodes: fileNodes[key]})
		seen[fa] = true
	}
	return CheckImplImports(program)[docFA]
}

// entryCandidates are the entry files the LSP builds a project from to
// analyze a file an entry imports: root/main.nomi, then each of nomi.toml's
// entry_points, without repeats.
func (dm *DocumentManager) entryCandidates(root string) []string {
	out := []string{filepath.Join(root, "main.nomi")}
	if mfst, err := LoadManifest(root); err == nil && mfst != nil {
		for _, e := range mfst.EntryPoints {
			path := filepath.Join(root, filepath.FromSlash(e)+".nomi")
			if path != out[0] {
				out = append(out, path)
			}
		}
	}
	return out
}

// relModuleKey converts an absolute file path under projectRoot into the
// "/"-joined module-path key BuildProjectWithCache uses to key its
// FileAnalysis cache. Strips the .nomi extension and uses forward
// slashes regardless of host OS (the cache key never contains backslashes).
//
//	root="/project", path="/project/greeter.nomi"          -> "greeter"
//	root="/project", path="/project/runtime/prod.nomi"     -> "runtime/prod"
func relModuleKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	rel = strings.TrimSuffix(rel, ".nomi")
	return filepath.ToSlash(rel)
}

// makeLoaderWithOverride returns a FileLoader that returns the given
// open-doc nodes when asked for the open doc's module path, and
// otherwise falls through to the standard makeLoader. This ensures
// in-flight edits in the open document drive the project build (rather
// than what's on disk) when we re-enter the project from main.nomi.
func (dm *DocumentManager) makeLoaderWithOverride(openURI string, openNodes []ast.Node) FileLoader {
	base := dm.makeLoader()
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		if "file://"+filePath == openURI {
			return openNodes, nil
		}
		return base(projectRoot, modulePath)
	}
}

// suppressInDamagedSpans drops the type diagnostics that fall inside a
// declaration the resilient parse had to repair.
//
// This is what a PARTIAL node means to everything downstream of the
// builder. The builder is asked to record a repaired declaration's scopes
// and symbols, because that is what completion needs at the cursor. It is
// not asked to vouch for the declaration: part of the body was never
// read, so the function's tail expression may be missing, a binding's
// initializer may be absent, a lambda's parameter may have nothing to
// infer from. Every one of those produces a type error that describes the
// parser's gap rather than anything the user wrote, and it would sit on
// the VALID half of the same function — a "return type mismatch" on the
// signature line, an "unused binding" on a line that reads fine.
//
// The rule is per top-level declaration rather than per statement,
// because the judgements that go wrong are whole-declaration ones. The
// syntax error itself is unaffected: it comes from Document.Errors, which
// is byte-identical to what a strict recovery parse reports. Declarations
// elsewhere in the file are checked and reported as usual.
func suppressInDamagedSpans(errs []TypeError, damaged []parser.Span) []TypeError {
	if len(damaged) == 0 || len(errs) == 0 {
		return errs
	}
	kept := make([]TypeError, 0, len(errs))
	for _, e := range errs {
		inDamage := false
		for _, span := range damaged {
			if span.Contains(e.Line, e.Col) {
				inDamage = true
				break
			}
		}
		if !inDamage {
			kept = append(kept, e)
		}
	}
	return kept
}
