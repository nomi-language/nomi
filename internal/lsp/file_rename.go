package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// fileRenameFilters are the file operations workspace/willRenameFiles is
// sent for: a .nomi file, and any folder (one may hold .nomi files).
func fileRenameFilters() []protocol.FileOperationFilter {
	scheme := "file"
	file := protocol.FileOperationPatternKindFile
	folder := protocol.FileOperationPatternKindFolder
	return []protocol.FileOperationFilter{
		{Scheme: &scheme, Pattern: protocol.FileOperationPattern{Glob: "**/*.nomi", Matches: &file}},
		{Scheme: &scheme, Pattern: protocol.FileOperationPattern{Glob: "**/*", Matches: &folder}},
	}
}

// workspaceWillRenameFiles answers a rename or move of .nomi files or of
// folders with the edits that keep every import of them in the workspace
// pointing at them: the import path, and where a path-only import without
// `as` binds the file under its last segment, each use of that qualifier
// (`users.make()` becomes `people.make()`).
func (s *Server) workspaceWillRenameFiles(gctx *glsp.Context, params *protocol.RenameFilesParams) (*protocol.WorkspaceEdit, error) {
	var moves []fileMove
	for _, f := range params.Files {
		oldPath, newPath := uriToPath(f.OldURI), uriToPath(f.NewURI)
		info, err := os.Stat(oldPath)
		dir := err == nil && info.IsDir()
		if !dir && (filepath.Ext(oldPath) != ".nomi" || filepath.Ext(newPath) != ".nomi") {
			continue
		}
		moves = append(moves, fileMove{oldPath: oldPath, newPath: newPath, dir: dir})
	}
	if len(moves) == 0 {
		return nil, nil
	}
	return importRenameEdits(s.docs, moves), nil
}

// fileMove is one renamed file or folder.
type fileMove struct {
	oldPath, newPath string
	dir              bool
}

// moved returns where path lands under the moves, if any moves it.
func moved(moves []fileMove, path string) (string, bool) {
	for _, m := range moves {
		if !m.dir {
			if path == m.oldPath {
				return m.newPath, true
			}
			continue
		}
		if rel, err := filepath.Rel(m.oldPath, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join(m.newPath, rel), true
		}
	}
	return "", false
}

// importRenameEdits computes the edits for moves over the workspace's open
// documents and its .nomi files on disk.
func importRenameEdits(dm *analysis.DocumentManager, moves []fileMove) *protocol.WorkspaceEdit {
	// An import of a moved file names its last segment, or for a folder
	// the folder's name, so a file without that text imports none of them.
	var needles []string
	for _, m := range moves {
		needles = append(needles, strings.TrimSuffix(filepath.Base(m.oldPath), ".nomi"))
	}
	paths := map[string]bool{}
	for _, uri := range dm.OpenURIs() {
		paths[uriToPath(uri)] = true
	}
	if root := dm.WorkspaceRoot(); root != "" {
		for _, p := range findNomiFiles(root) {
			paths[p] = true
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	changes := map[protocol.DocumentUri][]protocol.TextEdit{}
	for _, path := range sorted {
		uri := pathToURI(path)
		content, ok := documentText(dm, uri, path)
		if !ok || !containsAny(content, needles) {
			continue
		}
		edits := fileImportEdits(dm, uri, path, content, moves)
		if len(edits) == 0 {
			continue
		}
		lines := newLineIndex(content)
		for i := range edits {
			edits[i].Range = lines.utf16Range(edits[i].Range)
		}
		changes[protocol.DocumentUri(uri)] = edits
	}
	if len(changes) == 0 {
		return nil
	}
	return &protocol.WorkspaceEdit{Changes: changes}
}

// documentText is an open document's editor text, else the file's text on
// disk.
func documentText(dm *analysis.DocumentManager, uri, path string) (string, bool) {
	if dm.IsOpen(uri) {
		if snap := dm.Snapshot(uri); snap != nil {
			return snap.Content, true
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// importEntries lists a file's import statements, those inside an
// `import { ... }` block included.
func importEntries(nodes []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImportStmt:
			out = append(out, v)
		case *ast.ImportBlock:
			out = append(out, v.Entries...)
		}
	}
	return out
}

// importTarget resolves an import's path to the project file it names, as
// the analyzer does: against the importing file's project root, after
// stripping the module's own name when the path starts with it, taking
// the longest prefix of the path that names a file (the rest of a
// drill-through path, `models/users.User.{name}`, names owners inside
// it). It also returns the stripped prefix ("" or the module name) and n,
// the number of path segments that name the file. ok is false for a
// stdlib or Go import.
func importTarget(imp *ast.ImportStmt, root, moduleName string) (target, prefix string, n int, ok bool) {
	if imp.Extern || len(imp.ModulePath) == 0 {
		return "", "", 0, false
	}
	segs := make([]string, len(imp.ModulePath))
	for i, seg := range imp.ModulePath {
		segs[i] = ast.ImportNodeName(seg)
		if segs[i] == "" {
			return "", "", 0, false
		}
	}
	if segs[0] == "std" {
		return "", "", 0, false
	}
	path := segs
	if rest, self := analysis.SelfNameImport(segs, moduleName); self {
		path, prefix = rest, moduleName
	}
	skipped := len(segs) - len(path)
	for split := len(path); split >= 1; split-- {
		file := analysis.ResolveModulePath(root, path[:split])
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			return file, prefix, skipped + split, true
		}
	}
	return analysis.ResolveModulePath(root, path), prefix, len(segs), true
}

// importPathFor spells the import path of file path from root, or reports
// false when the file lies outside root or a segment is not a name an
// import path can hold.
func importPathFor(root, path string) ([]string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.Ext(rel) != ".nomi" {
		return nil, false
	}
	segs := strings.Split(strings.TrimSuffix(rel, ".nomi"), string(filepath.Separator))
	for _, seg := range segs {
		if !isImportSegment(seg) {
			return nil, false
		}
	}
	return segs, true
}

// isImportSegment reports whether seg lexes as one identifier.
func isImportSegment(seg string) bool {
	var toks []token.Token
	for _, t := range lexer.Lex(seg) {
		if t.Type != token.EOF && t.Type != token.NEWLINE {
			toks = append(toks, t)
		}
	}
	return len(toks) == 1 && (toks[0].Type == token.IDENT || toks[0].Type == token.TYPE_IDENT) && toks[0].Lexeme == seg
}

// fileImportEdits returns the byte-column edits one file needs for moves.
func fileImportEdits(dm *analysis.DocumentManager, uri, path, content string, moves []fileMove) []protocol.TextEdit {
	nodes, _, _ := parser.ParseResilient(lexer.Lex(content))
	imports := importEntries(nodes)
	if len(imports) == 0 {
		return nil
	}
	root := dm.FindProjectRoot(path)
	moduleName := ""
	if m, err := analysis.LoadManifest(root); err == nil && m != nil {
		moduleName = m.Name
	}
	var edits []protocol.TextEdit
	// requalify maps a path-only import's binding (its last segment's
	// position) to the qualifier it must be renamed to.
	requalify := map[analysis.Pos]string{}
	var pathRanges []protocol.Range
	for _, imp := range imports {
		target, prefix, n, ok := importTarget(imp, root, moduleName)
		if !ok {
			continue
		}
		newTarget, ok := moved(moves, target)
		if !ok {
			continue
		}
		segs, ok := importPathFor(root, newTarget)
		if !ok {
			continue
		}
		if prefix != "" {
			segs = append([]string{prefix}, segs...)
		}
		start, end := importPathRange(imp, n)
		r := protocol.Range{Start: start, End: end}
		pathRanges = append(pathRanges, r)
		edits = append(edits, protocol.TextEdit{Range: r, NewText: strings.Join(segs, "/")})

		oldLast := ast.ImportNodeName(imp.ModulePath[len(imp.ModulePath)-1])
		newLast := segs[len(segs)-1]
		if n == len(imp.ModulePath) && len(imp.Names) == 0 && !imp.IncludeParent && imp.ModuleAlias == nil && oldLast != newLast {
			line, col := identPos(imp.ModulePath[len(imp.ModulePath)-1])
			requalify[analysis.Pos{Line: line, Col: col}] = newLast
		}
	}
	if len(requalify) > 0 {
		edits = append(edits, qualifierEdits(dm, uri, requalify, pathRanges)...)
	}
	return edits
}

// qualifierEdits renames the uses of each file API object a path-only
// import binds, keyed by the binding's position, read from the file's
// analysis.
func qualifierEdits(dm *analysis.DocumentManager, uri string, requalify map[analysis.Pos]string, skip []protocol.Range) []protocol.TextEdit {
	snap := dm.Analyzed(uri)
	if snap == nil || snap.Analysis == nil {
		return nil
	}
	fa := snap.Analysis
	var edits []protocol.TextEdit
	for pos, sym := range fa.References {
		if analysis.IsSynthesizedLine(pos.Line) {
			continue
		}
		target := sym
		if target.Resolved != nil {
			target = target.Resolved
		}
		if target.Kind != analysis.SymbolModule {
			continue
		}
		newName, ok := requalify[target.Pos]
		if !ok || target.SourceFile != "" {
			continue
		}
		e := makeTextEdit(pos, target.Name, newName)
		inPath := false
		for _, r := range skip {
			if rangeContains(r, e.Range) {
				inPath = true
			}
		}
		if !inPath {
			edits = append(edits, e)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return posBefore(edits[i].Range.Start, edits[j].Range.Start) })
	return edits
}

func identPos(n ast.Node) (line, col int) {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Line, v.Col
	case *ast.TypeIdent:
		return v.Line, v.Col
	}
	return 0, 0
}

// importPathRange is the zero-based byte range of an import path's first
// n segments.
func importPathRange(imp *ast.ImportStmt, n int) (protocol.Position, protocol.Position) {
	fl, fc := identPos(imp.ModulePath[0])
	last := imp.ModulePath[n-1]
	ll, lc := identPos(last)
	return protocol.Position{Line: toZeroBased(fl), Character: toZeroBased(fc)},
		protocol.Position{Line: toZeroBased(ll), Character: toZeroBased(lc) + uint32(len(ast.ImportNodeName(last)))}
}
