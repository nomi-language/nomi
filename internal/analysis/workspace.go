package analysis

import (
	"container/list"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The workspace a client opens may hold hundreds of .nomi files. An editor
// holds a handful open. The manager analyzes and keeps only the open ones;
// for every other workspace file it keeps an index entry (IndexedFile)
// built from a parse, and it analyzes such a file only when a request needs
// its analysis, keeping the last closedCacheSize of those analyses.
//
// A full analysis of one file holds its nodes, scopes and the analyses of
// the project it was built in: several megabytes. Keeping one for every file
// under this repository's root held about 4 GB.

// IndexedFile is what the manager knows of a closed workspace file without
// analyzing it.
type IndexedFile struct {
	URI string
	// Root is the file's project root (ProjectRoot, bounded by the
	// workspace root).
	Root string
	// Imports are the absolute paths of the project files the file
	// imports, whether or not they exist.
	Imports []string
	// Decls are the file's top-level declarations, in source order.
	Decls []IndexedDecl
}

// IndexedDecl is one top-level declaration of an indexed file.
type IndexedDecl struct {
	Name   string
	Kind   SymbolKind
	Public bool
	// Pos is the declaration's name, the position its symbol's Pos holds.
	Pos Pos
	Doc string
	// TypeParams, Params and Return are a function's signature; nil for
	// any other declaration.
	TypeParams []ast.TypeParam
	Params     []ast.Param
	Return     ast.TypeExpr
}

// SkipWorkspaceDir reports whether the workspace scan leaves out a
// directory with this name. It follows the go tool's rule for package
// patterns: a directory whose name starts with "." or "_", or is named
// "testdata", holds no sources of the project; node_modules is the same
// for JavaScript tooling. Such files are still analyzed when opened, and a
// project rooted inside one indexes nothing from the scan.
func SkipWorkspaceDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		name == "testdata" || name == "node_modules"
}

// WorkspaceNomiFiles lists the .nomi files under root the workspace scan
// covers, in walk order: SkipWorkspaceDir prunes directories below root,
// and standard-library sources are left out, because the stdlib is
// analyzed once per process from its embedded copy.
func WorkspaceNomiFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && SkipWorkspaceDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".nomi" {
			return nil
		}
		if _, std := stdlibModuleForPath(path); std {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// IndexWorkspace indexes every workspace file (WorkspaceNomiFiles) that is
// not open, and returns their URIs. It parses one file at a time and
// yields between files; it stops when ctx ends.
func (dm *DocumentManager) IndexWorkspace(ctx context.Context) []string {
	root := dm.WorkspaceRoot()
	if root == "" {
		return nil
	}
	var uris []string
	for _, path := range WorkspaceNomiFiles(root) {
		if ctx.Err() != nil {
			return uris
		}
		if uri := "file://" + path; dm.IndexFile(uri) {
			uris = append(uris, uri)
		}
		runtime.Gosched()
	}
	return uris
}

// IndexFile reads uri's file from disk and indexes it, replacing its import
// edges, and drops the cached analyses its change makes stale. It does
// nothing to an open document, whose editor text wins, and reports false
// then or when the file cannot be read.
func (dm *DocumentManager) IndexFile(uri string) bool {
	if dm.IsOpen(uri) {
		return false
	}
	path := strings.TrimPrefix(uri, "file://")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	entry := dm.indexText(uri, string(data))
	dm.mu.Lock()
	if doc := dm.docs[uri]; doc != nil {
		// Opened while this parsed.
		dm.mu.Unlock()
		return false
	}
	dm.index[uri] = entry
	dm.setImportsLocked(uri, entry.Imports)
	dm.mu.Unlock()
	dm.invalidateClosed(uri)
	return true
}

// indexText builds the index entry of uri's text.
func (dm *DocumentManager) indexText(uri, content string) *IndexedFile {
	nodes, _, _ := parser.ParseResilient(lexer.Lex(content))
	root := dm.projectRootFromURI(uri)
	return &IndexedFile{
		URI:     uri,
		Root:    root,
		Imports: ExtractImportsFromNodes(nodes, root),
		Decls:   TopLevelDecls(nodes),
	}
}

// TopLevelDecls lists the declarations of a file's top-level nodes.
func TopLevelDecls(nodes []ast.Node) []IndexedDecl {
	var out []IndexedDecl
	for _, n := range nodes {
		var d IndexedDecl
		switch n := n.(type) {
		case *ast.FuncDef:
			d = IndexedDecl{Name: n.Name, Kind: SymbolFunction, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc,
				TypeParams: n.TypeParams, Params: n.Params, Return: n.ReturnTypeExpr}
		case *ast.ExternFunc:
			d = IndexedDecl{Name: n.Name, Kind: SymbolFunction, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc,
				TypeParams: n.TypeParams, Params: n.Params, Return: n.ReturnTypeExpr}
		case *ast.StructDef:
			d = IndexedDecl{Name: n.Name, Kind: SymbolStruct, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.EnumDef:
			d = IndexedDecl{Name: n.Name, Kind: SymbolEnum, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.TypeDef:
			d = IndexedDecl{Name: n.Name, Kind: SymbolType, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.ExternType:
			d = IndexedDecl{Name: n.Name, Kind: SymbolType, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.TypeAlias:
			d = IndexedDecl{Name: n.Name, Kind: SymbolTypeAlias, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.InterfaceDef:
			d = IndexedDecl{Name: n.Name, Kind: SymbolInterface, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		case *ast.OnceBinding:
			d = IndexedDecl{Name: n.Name, Kind: SymbolOnce, Public: n.Public, Pos: Pos{Line: n.Line, Col: n.Col}, Doc: n.Doc}
		default:
			continue
		}
		if d.Name != "" {
			out = append(out, d)
		}
	}
	return out
}

// Indexed returns the index entries of the closed files under dir, sorted
// by URI. An empty dir means every entry.
func (dm *DocumentManager) Indexed(dir string) []*IndexedFile {
	prefix := ""
	if dir != "" {
		prefix = "file://" + strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	}
	dm.mu.RLock()
	var out []*IndexedFile
	for uri, entry := range dm.index {
		if _, open := dm.docs[uri]; !open && strings.HasPrefix(uri, prefix) {
			out = append(out, entry)
		}
	}
	dm.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out
}

// FileDecls returns the top-level declarations of the file at path: the
// open document's latest analyzed nodes, the index entry of a closed file,
// or a parse of the file on disk, which is not kept.
func (dm *DocumentManager) FileDecls(path string) []IndexedDecl {
	uri := "file://" + path
	dm.mu.RLock()
	doc, open := dm.docs[uri]
	var nodes []ast.Node
	if open {
		nodes = doc.Nodes
	}
	entry := dm.index[uri]
	dm.mu.RUnlock()
	switch {
	case open:
		return TopLevelDecls(nodes)
	case entry != nil:
		return entry.Decls
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	nodes, _, _ = parser.ParseResilient(lexer.Lex(string(data)))
	return TopLevelDecls(nodes)
}

// RemoveWorkspaceFile forgets a deleted file: its index entry, its cached
// analysis and its import edges. The files that import it keep their edges
// to it, so creating it again reaches them.
func (dm *DocumentManager) RemoveWorkspaceFile(uri string) {
	dm.invalidateClosed(uri)
	dm.mu.Lock()
	delete(dm.docs, uri)
	delete(dm.index, uri)
	dm.setImportsLocked(uri, nil)
	dm.mu.Unlock()
}

// closedCacheSize bounds the analyses of closed files the manager keeps for
// requests. Each holds the analyses of the project it was built in.
const closedCacheSize = 32

// closedEntry is one cached analysis of a closed file's disk text.
type closedEntry struct {
	uri     string
	modTime time.Time
	size    int64
	snap    *DocSnapshot
}

// closedCache is an LRU of closed files' analyses, guarded by dm.mu.
type closedCache struct {
	order *list.List // of *closedEntry, most recently used first
	byURI map[string]*list.Element
}

func (c *closedCache) get(uri string) *closedEntry {
	if c.byURI == nil {
		return nil
	}
	el := c.byURI[uri]
	if el == nil {
		return nil
	}
	c.order.MoveToFront(el)
	return el.Value.(*closedEntry)
}

func (c *closedCache) put(e *closedEntry) {
	if c.byURI == nil {
		c.byURI = map[string]*list.Element{}
		c.order = list.New()
	}
	c.remove(e.uri)
	c.byURI[e.uri] = c.order.PushFront(e)
	for c.order.Len() > closedCacheSize {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.byURI, last.Value.(*closedEntry).uri)
	}
}

func (c *closedCache) remove(uri string) {
	if el := c.byURI[uri]; el != nil {
		c.order.Remove(el)
		delete(c.byURI, uri)
	}
}

func (c *closedCache) clear() {
	c.order, c.byURI = nil, nil
}

// Analyzed returns a snapshot of uri with an analysis: the open document's
// (whose Analysis is nil until its first analysis finishes), or for a
// closed file an analysis of its disk text, built now unless one from the
// same text is cached. It returns nil when uri is neither open nor
// readable. A request calls it, so a build here goes ahead of background
// ones.
func (dm *DocumentManager) Analyzed(uri string) *DocSnapshot {
	if snap := dm.Snapshot(uri); snap != nil {
		return snap
	}
	path := strings.TrimPrefix(uri, "file://")
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	dm.mu.Lock()
	if e := dm.closed.get(uri); e != nil && e.modTime.Equal(info.ModTime()) && e.size == info.Size() {
		dm.mu.Unlock()
		return e.snap
	}
	dm.mu.Unlock()
	snap := dm.buildClosed(uri, true)
	if snap == nil {
		return nil
	}
	dm.mu.Lock()
	if _, open := dm.docs[uri]; !open {
		dm.closed.put(&closedEntry{uri: uri, modTime: info.ModTime(), size: info.Size(), snap: snap})
	}
	dm.mu.Unlock()
	return snap
}

// AnalyzeClosed builds an analysis of a closed file's disk text and returns
// it without keeping it, for its diagnostics. It returns nil when uri is
// open or unreadable. It waits for builds requests and edits need, so it
// never delays one by more than one build.
func (dm *DocumentManager) AnalyzeClosed(uri string) *DocSnapshot {
	if dm.IsOpen(uri) {
		return nil
	}
	return dm.buildClosed(uri, false)
}

// buildClosed analyzes the disk text of uri as an open document's text
// would be analyzed.
func (dm *DocumentManager) buildClosed(uri string, foreground bool) *DocSnapshot {
	data, err := os.ReadFile(strings.TrimPrefix(uri, "file://"))
	if err != nil {
		return nil
	}
	content := string(data)
	dm.lockBuild(foreground)
	// Deferred: a panic in build, which the language server recovers,
	// must not leave every later build waiting on analyzeMu.
	defer dm.analyzeMu.Unlock()
	b := dm.build(uri, content)
	return &DocSnapshot{
		URI:             uri,
		Content:         content,
		Text:            content,
		Nodes:           b.nodes,
		Errors:          b.errs,
		Damaged:         b.damaged,
		Analysis:        b.fa,
		Version:         1,
		AnalyzedVersion: 1,
	}
}

// invalidateClosed drops the cached analyses a change to uri makes stale:
// uri's own and those of every file that imports it, transitively.
func (dm *DocumentManager) invalidateClosed(uri string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed.byURI == nil {
		return
	}
	seen := map[string]bool{}
	queue := []string{uri}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		dm.closed.remove(u)
		queue = append(queue, dm.reverseDeps[u]...)
	}
}

// lockBuild takes the build lock. A foreground build (an edit's or a
// request's) takes it as soon as it is free; a background one first waits
// until no foreground build is waiting or running.
func (dm *DocumentManager) lockBuild(foreground bool) {
	if foreground {
		dm.foreground.Add(1)
		dm.analyzeMu.Lock()
		dm.foreground.Add(-1)
		return
	}
	for {
		for dm.foreground.Load() > 0 {
			time.Sleep(2 * time.Millisecond)
		}
		dm.analyzeMu.Lock()
		if dm.foreground.Load() == 0 {
			return
		}
		dm.analyzeMu.Unlock()
	}
}

// ForegroundBusy reports whether a foreground build is waiting or running.
func (dm *DocumentManager) ForegroundBusy() bool {
	return dm.foreground.Load() > 0
}

// setImportsLocked replaces uri's import edges with imports (absolute
// paths). The caller holds dm.mu.
func (dm *DocumentManager) setImportsLocked(uri string, imports []string) {
	for _, old := range dm.imports[uri] {
		deps := dm.reverseDeps[old]
		kept := deps[:0]
		for _, d := range deps {
			if d != uri {
				kept = append(kept, d)
			}
		}
		if len(kept) == 0 {
			delete(dm.reverseDeps, old)
		} else {
			dm.reverseDeps[old] = kept
		}
	}
	if len(imports) == 0 {
		delete(dm.imports, uri)
		return
	}
	uris := make([]string, 0, len(imports))
	for _, imp := range imports {
		impURI := "file://" + imp
		uris = append(uris, impURI)
		dm.reverseDeps[impURI] = append(dm.reverseDeps[impURI], uri)
	}
	dm.imports[uri] = uris
}

// OpenURIs returns the open documents' URIs, sorted.
func (dm *DocumentManager) OpenURIs() []string {
	dm.mu.RLock()
	out := make([]string, 0, len(dm.docs))
	for uri := range dm.docs {
		out = append(out, uri)
	}
	dm.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Importers returns the closed files that import uri, transitively, and
// drops their cached analyses.
func (dm *DocumentManager) Importers(uri string) []string {
	dm.invalidateClosed(uri)
	var out []string
	seen := map[string]bool{uri: true}
	queue := dm.ReverseDeps(uri)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		if !dm.IsOpen(u) {
			out = append(out, u)
		}
		queue = append(queue, dm.ReverseDeps(u)...)
	}
	return out
}

// Covers reports whether the workspace scan covers uri's file: a .nomi
// file under the workspace root, in no directory SkipWorkspaceDir leaves
// out, and not a stdlib source. A watcher event for any other file updates
// no index entry.
func (dm *DocumentManager) Covers(uri string) bool {
	root := dm.WorkspaceRoot()
	path := strings.TrimPrefix(uri, "file://")
	if root == "" || filepath.Ext(path) != ".nomi" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, dir := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if dir != "." && SkipWorkspaceDir(dir) {
			return false
		}
	}
	_, std := stdlibModuleForPath(path)
	return !std
}
