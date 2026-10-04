package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// codeActionKindSourceFixAll is the LSP 3.16 `source.fixAll` kind. glsp 0.2.2
// binds `source` and `source.organizeImports` but not `source.fixAll`, so we
// name it here.
const codeActionKindSourceFixAll = protocol.CodeActionKind("source.fixAll")

// buildOrganizeImportsAction returns a `source.organizeImports` code action that
// removes every unused import and re-renders the file's import region in
// canonical form (sorted, self-first, single-entry blocks collapsed) — the
// "do it all at once" companion to the per-item remove/add quick-fixes.
//
// It returns nil (no action) in any case where the single whole-region replace
// would be unsafe or pointless:
//   - no imports;
//   - the rendered result equals the current text (nothing to do);
//   - a comment sits inside the import region (the replace would clobber it);
//   - a non-import declaration is interleaved between imports (Nomi resolves
//     whole-program, so a late `import` is legal — the region would then span
//     real code that the replace would delete).
//
// These bails are the deliberate v1 limit: organize replaces the whole region
// in one edit, so wherever that region contains anything other than imports we
// decline rather than destroy it. (Leading `//#`/header comments sit ABOVE the
// region and are unaffected.)
//
// Canonical layout note: the rendered region uses single-line separation, so a
// user-written blank line between import groups is intentionally collapsed
// (organize re-sorts across the whole set). The result is stable — `nomi fmt`
// won't re-add the blank — but organize and fmt can differ on group spacing.
func buildOrganizeImportsAction(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string) *protocol.CodeAction {
	edit, ok := importFixEdit(content, nodes, fa, false)
	if !ok {
		return nil
	}
	return importRewriteAction("Organize imports", protocol.CodeActionKindSourceOrganizeImports, uri, edit)
}

// buildFixAllImportsAction returns a `source.fixAll` code action that removes
// unused imports AND adds every missing qualified import (`io.inspect` with no
// `std/io`) in one pass, then canonicalizes — the symmetric, goimports-style
// companion to organize. It is the action to wire to run on save: because both
// halves run, the comment-out-to-test loop round-trips losslessly (the import
// is removed when its last use goes away and re-added when the use returns).
//
// Only the *qualified* add is automated here — it is unambiguous (a colliding
// name would already resolve, so no aliasing is ever needed). Bare-name add
// stays a manual quick-fix: it can be ambiguous (two modules exporting one
// name) and must not be resolved silently on save.
//
// The same region-safety bails as organize apply (comment / interleaved code).
func buildFixAllImportsAction(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string) *protocol.CodeAction {
	edit, ok := importFixEdit(content, nodes, fa, true)
	if !ok {
		return nil
	}
	return importRewriteAction("Fix all imports (remove unused, add missing)", codeActionKindSourceFixAll, uri, edit)
}

func importRewriteAction(title string, kind protocol.CodeActionKind, uri string, edit protocol.TextEdit) *protocol.CodeAction {
	return &protocol.CodeAction{
		Title: title,
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{
				protocol.DocumentUri(uri): {edit},
			},
		},
	}
}

// importFixEdit computes the single TextEdit that rewrites the file's imports:
// always pruning unused + canonicalizing, and — when withAdd — also adding every
// missing qualified import. ok is false when there is nothing to do or the
// region can't be safely replaced (comment or interleaved code).
//
// When the file has no import region at all, the only possible fix is adding
// (withAdd): the new imports are inserted at the top.
func importFixEdit(content string, nodes []ast.Node, fa *analysis.FileAnalysis, withAdd bool) (protocol.TextEdit, bool) {
	lineOffs := lineOffsets(content)
	start, end, ok := importRegionBounds(content, lineOffs, nodes)
	if ok {
		if strings.Contains(content[start:end], "//") {
			return protocol.TextEdit{}, false
		}
		if !importsContiguous(lineOffs, nodes, start, end) {
			return protocol.TextEdit{}, false
		}
		set := pruneUnusedImports(nodes, fa)
		if withAdd {
			set = addMissingQualifiedImports(set, nodes, fa)
		}
		newText, err := format.RenderImports(set)
		if err != nil || newText == content[start:end] {
			return protocol.TextEdit{}, false
		}
		return rangeEdit(content, start, end, newText), true
	}
	if !withAdd {
		return protocol.TextEdit{}, false
	}
	missing := unambiguousMissing(analysis.FindMissingImports(fa, nodes))
	if len(missing) == 0 {
		return protocol.TextEdit{}, false
	}
	stmts := make([]ast.Node, len(missing))
	for i, m := range missing {
		stmts[i] = importStmtFor(m)
	}
	newText, err := format.RenderImports(stmts)
	if err != nil {
		return protocol.TextEdit{}, false
	}
	return importInsertEdit(content, lineOffs, nodes, newText), true
}

// addMissingQualifiedImports folds every UNAMBIGUOUS missing import into the
// pruned set: merging into an existing same-module statement (self for a module
// qualifier, the type name for a selective import) when present, else appending
// a new statement — as a block entry when the file uses a block, preserving its
// grouping style so a remove-then-add round-trips to the same block. Ambiguous
// type names (exported by 2+ modules) are left out — a save must not silently
// pick one. Existing nodes are never mutated (merges clone; RenderImports clones
// again before sorting).
func addMissingQualifiedImports(set []ast.Node, nodes []ast.Node, fa *analysis.FileAnalysis) []ast.Node {
	missing := unambiguousMissing(analysis.FindMissingImports(fa, nodes))
	if len(missing) == 0 {
		return set
	}
	out := append([]ast.Node(nil), set...)
	for _, m := range missing {
		if applyMissingToSet(out, m) {
			continue
		}
		stmt := importStmtFor(m)
		if block := firstImportBlock(out); block != nil {
			block.Entries = append(block.Entries, stmt)
		} else {
			out = append(out, stmt)
		}
	}
	return out
}

// applyMissingToSet merges m into an existing same-module statement already in
// out — a top-level statement or a block entry, in place. Returns true when an
// existing statement handles m (merged, or already satisfied) so the caller does
// not also append it.
func applyMissingToSet(out []ast.Node, m analysis.MissingImport) bool {
	segs := strings.Split(m.ModulePath, "/")
	for i, node := range out {
		switch v := node.(type) {
		case *ast.ImportStmt:
			if importStmtPathMatches(v, segs) {
				if merged, ok := mergeInto(v, m); ok {
					out[i] = merged
				}
				return true
			}
		case *ast.ImportBlock:
			for j, e := range v.Entries {
				if importStmtPathMatches(e, segs) {
					if merged, ok := mergeInto(e, m); ok {
						v.Entries[j] = merged
					}
					return true
				}
			}
		}
	}
	return false
}

// unambiguousMissing keeps only suggestions at positions with a single candidate
// module (dropping ambiguous type names), deduplicated by import spec.
func unambiguousMissing(missing []analysis.MissingImport) []analysis.MissingImport {
	count := map[analysis.Pos]int{}
	for _, m := range missing {
		count[m.Pos]++
	}
	seen := map[string]bool{}
	var out []analysis.MissingImport
	for _, m := range missing {
		if count[m.Pos] != 1 || seen[m.ImportSpec()] {
			continue
		}
		seen[m.ImportSpec()] = true
		out = append(out, m)
	}
	return out
}

// importStmtFor builds the import statement m adds: selective `import std/io`
// (module qualifier) or selective `import std/sets.Set` (type).
func importStmtFor(m analysis.MissingImport) *ast.ImportStmt {
	stmt := &ast.ImportStmt{ModulePath: identPath(strings.Split(m.ModulePath, "/"))}
	if m.Member != "" {
		stmt.Names = []ast.Node{&ast.TypeIdent{Name: m.Member}}
	}
	return stmt
}

// firstImportBlock returns the first ImportBlock among nodes, or nil.
func firstImportBlock(nodes []ast.Node) *ast.ImportBlock {
	for _, n := range nodes {
		if b, ok := n.(*ast.ImportBlock); ok {
			return b
		}
	}
	return nil
}

// importRegionBounds returns the [start, end) byte span the file's top-level
// import constructs occupy — from the first construct's first line to the last
// construct's final line, excluding that line's trailing newline. ok is false
// when the file has no (non-synthesized) imports.
func importRegionBounds(content string, lineOffs []int, nodes []ast.Node) (int, int, bool) {
	firstLine, lastLine := -1, -1
	note := func(startLine, endLine int) {
		if firstLine < 0 || startLine < firstLine {
			firstLine = startLine
		}
		if endLine > lastLine {
			lastLine = endLine
		}
	}
	for _, node := range nodes {
		switch v := node.(type) {
		case *ast.ImportStmt:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			_, e := statementBounds(content, lineOffs, v)
			note(v.Line-1, lineIndexOf(lineOffs, e-1))
		case *ast.ImportBlock:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			closeOff := matchBraceEnd(content, posToOffset(lineOffs, v.Line, v.Col))
			note(v.Line-1, lineIndexOf(lineOffs, closeOff))
		}
	}
	if firstLine < 0 {
		return 0, 0, false
	}
	start := lineOffs[firstLine]
	end := len(content)
	if lastLine+1 < len(lineOffs) {
		end = lineOffs[lastLine+1] - 1 // the newline terminating the last line
	}
	return start, end, true
}

// importsContiguous reports whether the import region [start, end) contains
// only import constructs — i.e. no other top-level declaration's line falls
// inside it. Imports may legally appear after other declarations in Nomi, so
// the first-import-to-last-import span can otherwise straddle real code that
// the whole-region replace would delete.
func importsContiguous(lineOffs []int, nodes []ast.Node, start, end int) bool {
	for _, n := range nodes {
		switch n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			continue
		}
		ln := n.LineNum()
		if ln <= 0 {
			continue
		}
		off := posToOffset(lineOffs, ln, 1)
		if off >= start && off <= end {
			return false
		}
	}
	return true
}

// pruneInfo records which items of one import statement are unused.
type pruneInfo struct {
	whole bool         // internal/recovered empty-selector import
	self  bool         // the `self` marker is unused
	idx   map[int]bool // unused Names indices
}

// pruneUnusedImports returns the file's top-level import constructs (in source
// order) with every removable item dropped: dead brace items, dead `self`,
// items the prelude already provides, and statements/blocks that become empty
// omitted entirely. Both diagnostic classes report []analysis.UnusedImport, so
// they prune through one path.
func pruneUnusedImports(nodes []ast.Node, fa *analysis.FileAnalysis) []ast.Node {
	info := map[*ast.ImportStmt]*pruneInfo{}
	removable := append(analysis.FindUnusedImports(fa, nodes),
		analysis.FindRedundantPreludeImports(fa, nodes)...)
	for _, u := range removable {
		pi := info[u.Stmt]
		if pi == nil {
			pi = &pruneInfo{idx: map[int]bool{}}
			info[u.Stmt] = pi
		}
		switch u.ItemKind {
		case analysis.UnusedWholeModule, analysis.UnusedModuleAlias:
			pi.whole = true
		case analysis.UnusedBraceItem:
			pi.idx[u.NameIdx] = true
		case analysis.UnusedSelfMarker:
			pi.self = true
		}
	}
	var out []ast.Node
	for _, node := range nodes {
		switch v := node.(type) {
		case *ast.ImportStmt:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			if ps, keep := pruneStmt(v, info[v]); keep {
				out = append(out, ps)
			}
		case *ast.ImportBlock:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			var kept []*ast.ImportStmt
			for _, e := range v.Entries {
				if ps, keep := pruneStmt(e, info[e]); keep {
					kept = append(kept, ps)
				}
			}
			if len(kept) > 0 {
				b := *v
				b.Entries = kept
				out = append(out, &b)
			}
		}
	}
	return out
}

// pruneStmt returns stmt with its unused items removed, and whether the
// statement survives at all (false ⇒ drop it).
func pruneStmt(stmt *ast.ImportStmt, pi *pruneInfo) (*ast.ImportStmt, bool) {
	if pi == nil {
		return stmt, true
	}
	if pi.whole {
		return nil, false
	}
	if len(stmt.Names) > 0 {
		c := *stmt
		var names, aliases, exportAliases []ast.Node
		var flags []bool
		for i, n := range stmt.Names {
			if pi.idx[i] {
				continue
			}
			names = append(names, n)
			aliases = append(aliases, itemAt(stmt.Aliases, i))
			exportAliases = append(exportAliases, itemAt(stmt.ExportAliases, i))
			flags = append(flags, flagAt(stmt.ExportFlags, i))
		}
		newSelf := stmt.IncludeParent && !pi.self
		if len(names) == 0 && !newSelf {
			return nil, false
		}
		c.Names = names
		c.Aliases = aliases
		c.ExportAliases = exportAliases
		c.ExportFlags = flags
		c.IncludeParent = newSelf
		return &c, true
	}
	// No brace names: a `{self}`-only statement, or an internal/recovered empty
	// selector import (which would have been pi.whole). Drop only when its sole
	// binding, self, is dead.
	if stmt.IncludeParent && pi.self {
		return nil, false
	}
	return stmt, true
}

func itemAt(s []ast.Node, i int) ast.Node {
	if i < len(s) {
		return s[i]
	}
	return nil
}

func flagAt(s []bool, i int) bool {
	if i < len(s) {
		return s[i]
	}
	return false
}
