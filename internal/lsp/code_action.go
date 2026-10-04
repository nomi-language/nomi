package lsp

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentCodeAction answers `textDocument/codeAction`. It emits two
// quick-fix families, each keyed by a diagnostic the client reports over the
// requested range:
//
//   - "Remove unused import" for each unused-import diagnostic — a surgical
//     edit that removes exactly the dead item (and at most one adjacent comma),
//     never reformatting the rest of the import block.
//   - "Add import <path>" for each module-qualifier (`io.inspect`) whose
//     stdlib module the file never imported — either merging `self` into an
//     existing selective import of that module or inserting a new statement.
//   - the fill fixes (code_action_fill.go): missing impl functions, struct
//     literal fields and case arms.
//
// and the actions over the requested range (code_action_refactor.go).
func (s *Server) textDocumentCodeAction(_ *glsp.Context, params *protocol.CodeActionParams) (any, error) {
	uri := string(params.TextDocument.URI)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		return nil, nil
	}
	only := params.Context.Only
	var actions []protocol.CodeAction
	if kindAllowed(only, protocol.CodeActionKindQuickFix) {
		actions = append(actions, buildRemoveUnusedActions(snap.Content, snap.Nodes, snap.Analysis, uri, params.Context.Diagnostics)...)
		actions = append(actions, buildAddImportActions(snap.Content, snap.Nodes, snap.Analysis, uri, params.Context.Diagnostics)...)
		actions = append(actions, s.buildFillActions(snap.Content, snap.Nodes, snap.Analysis, uri, params.Context.Diagnostics)...)
	}
	actions = append(actions, s.buildRefactorActions(snap.Content, snap.Nodes, snap.Analysis, uri, params.Range, params.Context.Diagnostics, only)...)
	if kindAllowed(only, protocol.CodeActionKindSourceOrganizeImports) {
		if a := buildOrganizeImportsAction(snap.Content, snap.Nodes, snap.Analysis, uri); a != nil {
			actions = append(actions, *a)
		}
	}
	if kindAllowed(only, codeActionKindSourceFixAll) {
		if a := buildFixAllImportsAction(snap.Content, snap.Nodes, snap.Analysis, uri); a != nil {
			actions = append(actions, *a)
		}
	}
	if len(actions) == 0 {
		return nil, nil
	}
	return actions, nil
}

// kindAllowed reports whether an action of the given kind should be offered for
// a request narrowed to `only`. An empty `only` means the client wants every
// kind. Otherwise a requested kind matches when it equals the action kind or is
// one of its dotted prefixes (LSP semantics: "source" matches
// "source.organizeImports").
func kindAllowed(only []protocol.CodeActionKind, kind protocol.CodeActionKind) bool {
	if len(only) == 0 {
		return true
	}
	for _, o := range only {
		if o == "" || o == kind || strings.HasPrefix(string(kind), string(o)+".") {
			return true
		}
	}
	return false
}

// buildRemoveUnusedActions returns one removal quick-fix per unused-import or
// redundant-prelude-import diagnostic in diags. Both classes describe an
// import item that can be deleted without changing what the file means, and
// both are reported as []analysis.UnusedImport, so one edit path serves them.
// It is the testable core of the handler: it takes the document content /
// nodes / analysis directly rather than going through the Server, so tests can
// drive it without a live server.
func buildRemoveUnusedActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	if len(diags) == 0 {
		return nil
	}
	// The structured query is the authoritative set of unused imports; we
	// match diagnostics to it by position. Position-matching (rather than
	// code-matching alone) is required because glsp's IntegerOrString uses a
	// value-receiver UnmarshalJSON, so the diagnostic's Code is silently
	// dropped when the client echoes it back in the codeAction request —
	// strict code-filtering would then yield no fixes in real use.
	unused := append(analysis.FindUnusedImports(fa, nodes),
		analysis.FindRedundantPreludeImports(fa, nodes)...)
	if len(unused) == 0 {
		return nil
	}
	lines := newLineIndex(content)
	var actions []protocol.CodeAction
	for _, diag := range diags {
		if !diagAllowsUnusedFix(diag) {
			continue
		}
		u, ok := matchUnused(lines, unused, diag)
		if !ok {
			continue
		}
		edit, ok := unusedImportEdit(content, u)
		if !ok {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       removalTitle(diag),
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{diag},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {edit},
				},
			},
		})
	}
	return actions
}

// removalTitle names the fix after the reason the item can go, so the two
// classes stay distinguishable in the editor's lightbulb menu. The code can be
// absent on the round-trip (see buildRemoveUnusedActions), in which case the
// unused wording is the safe default — it is the older and more common class.
func removalTitle(diag protocol.Diagnostic) string {
	if diag.Code != nil {
		if v, ok := diag.Code.Value.(string); ok && v == analysis.RedundantPreludeImportCode {
			return "Remove redundant prelude import"
		}
	}
	return "Remove unused import"
}

// diagAllowsUnusedFix reports whether diag's code is compatible with a removal
// fix: it must be the unused-import code, the redundant-prelude-import code,
// or absent. A code is treated as "absent" when the field is nil OR its value
// is nil/empty — the latter covering glsp's lossy round-trip (see
// buildRemoveUnusedActions). A different, non-empty code (some other
// diagnostic class at an import position) is rejected. The authoritative gate
// is still the position match.
func diagAllowsUnusedFix(diag protocol.Diagnostic) bool {
	if diag.Code == nil {
		return true
	}
	switch v := diag.Code.Value.(type) {
	case nil:
		return true
	case string:
		return v == "" || v == analysis.UnusedImportCode || v == analysis.RedundantPreludeImportCode
	default:
		return false
	}
}

// matchUnused finds the structured UnusedImport whose 1-based position lies
// in the diagnostic's range.
func matchUnused(lines *lineIndex, unused []analysis.UnusedImport, diag protocol.Diagnostic) (analysis.UnusedImport, bool) {
	for _, u := range unused {
		if diagCovers(lines, diag, u.Pos.Line, u.Pos.Col) {
			return u, true
		}
	}
	return analysis.UnusedImport{}, false
}

// unusedImportEdit computes the surgical TextEdit removing u from content.
func unusedImportEdit(content string, u analysis.UnusedImport) (protocol.TextEdit, bool) {
	if u.Stmt == nil {
		return protocol.TextEdit{}, false
	}
	lineOffs := lineOffsets(content)
	switch u.ItemKind {
	case analysis.UnusedWholeModule, analysis.UnusedModuleAlias:
		// Internal/recovered empty-selector import: the statement's only
		// binding is dead, so drop the whole line.
		return deleteWholeStatementEdit(content, lineOffs, u.Stmt), true

	case analysis.UnusedBraceItem:
		return braceItemEdit(content, lineOffs, u)

	case analysis.UnusedSelfMarker:
		return selfMarkerEdit(content, lineOffs, u)
	}
	return protocol.TextEdit{}, false
}

// braceItemEdit removes one selective `{...}` name.
func braceItemEdit(content string, lineOffs []int, u analysis.UnusedImport) (protocol.TextEdit, bool) {
	stmt := u.Stmt
	if u.NameIdx < 0 || u.NameIdx >= len(stmt.Names) {
		return protocol.TextEdit{}, false
	}
	if len(stmt.Names) == 1 {
		if stmt.IncludeParent {
			// `{self, X}` with X the lone Names entry now dead: a lone
			// `{self}` keeps only the parent binding.
			return replaceWithSelfOnlyImportEdit(content, lineOffs, stmt)
		}
		// Sole brace item, no self: an empty `{}` isn't valid — drop the
		// whole statement.
		return deleteWholeStatementEdit(content, lineOffs, stmt), true
	}
	if len(stmt.Names) == 2 && !stmt.IncludeParent {
		keptIdx := 0
		if u.NameIdx == 0 {
			keptIdx = 1
		}
		c := *stmt
		c.Names = []ast.Node{stmt.Names[keptIdx]}
		c.Aliases = nodeAt(stmt.Aliases, keptIdx)
		c.ExportFlags = boolAt(stmt.ExportFlags, keptIdx)
		c.ExportAliases = nodeAt(stmt.ExportAliases, keptIdx)
		start, end := statementBounds(content, lineOffs, stmt)
		return rangeEdit(content, start, end, format.RenderImportBody(&c)), true
	}
	// One of several: delete the item plus one adjacent comma.
	itemStart, itemEnd, ok := braceItemSpan(lineOffs, stmt, u.NameIdx)
	if !ok {
		return protocol.TextEdit{}, false
	}
	delStart, delEnd := removeWithComma(content, itemStart, itemEnd)
	return rangeEdit(content, delStart, delEnd, ""), true
}

// selfMarkerEdit removes the `self` item from a `{self, ...}` brace list.
func selfMarkerEdit(content string, lineOffs []int, u analysis.UnusedImport) (protocol.TextEdit, bool) {
	stmt := u.Stmt
	if !stmt.IncludeParent || stmt.SelfLine <= 0 {
		return protocol.TextEdit{}, false
	}
	if len(stmt.Names) == 0 {
		// `{self}` alone: self is the statement's only binding — drop it.
		return deleteWholeStatementEdit(content, lineOffs, stmt), true
	}
	selfStart := posToOffset(lineOffs, stmt.SelfLine, stmt.SelfCol)
	selfEnd := selfStart + len("self")
	delStart, delEnd := removeWithComma(content, selfStart, selfEnd)
	return rangeEdit(content, delStart, delEnd, ""), true
}

// replaceWithSelfOnlyImportEdit rewrites a brace statement down to a `{self}`-
// only import, preserving the parent binding after all named children are gone.
func replaceWithSelfOnlyImportEdit(content string, lineOffs []int, stmt *ast.ImportStmt) (protocol.TextEdit, bool) {
	start, end := statementBounds(content, lineOffs, stmt)
	c := *stmt
	c.Names = nil
	c.Aliases = nil
	c.ExportFlags = nil
	c.ExportAliases = nil
	c.IncludeParent = true
	return rangeEdit(content, start, end, format.RenderImportBody(&c)), true
}

// deleteWholeStatementEdit removes every line the statement occupies, trailing
// newline included.
func deleteWholeStatementEdit(content string, lineOffs []int, stmt *ast.ImportStmt) protocol.TextEdit {
	start, end := statementBounds(content, lineOffs, stmt)
	startLine := lineIndexOf(lineOffs, start)
	endLine := lineIndexOf(lineOffs, end-1)
	delStart := lineOffs[startLine]
	delEnd := len(content)
	if endLine+1 < len(lineOffs) {
		delEnd = lineOffs[endLine+1]
	}
	return rangeEdit(content, delStart, delEnd, "")
}

// braceItemSpan returns the [start, end) byte offsets of one brace item,
// covering its alias when present (`Foo as Bar`).
func braceItemSpan(lineOffs []int, stmt *ast.ImportStmt, idx int) (int, int, bool) {
	nameNode := stmt.Names[idx]
	start, ok := importNodeStart(lineOffs, nameNode)
	if !ok {
		return 0, 0, false
	}
	end, _ := importNodeEnd(lineOffs, nameNode)
	if idx < len(stmt.Aliases) && stmt.Aliases[idx] != nil {
		if aliasEnd, ok := importNodeEnd(lineOffs, stmt.Aliases[idx]); ok {
			end = aliasEnd
		}
	}
	return start, end, true
}

// removeWithComma expands an item span to also swallow exactly one adjacent
// comma: the preceding `, ` when present (so `{A, B}` minus B → `{A}`),
// otherwise the following `, ` (so `{A, B}` minus A → `{B}`).
func removeWithComma(content string, itemStart, itemEnd int) (int, int) {
	j := itemStart - 1
	for j >= 0 && (content[j] == ' ' || content[j] == '\t') {
		j--
	}
	if j >= 0 && content[j] == ',' {
		return j, itemEnd
	}
	k := itemEnd
	for k < len(content) && (content[k] == ' ' || content[k] == '\t') {
		k++
	}
	if k < len(content) && content[k] == ',' {
		k++
		for k < len(content) && (content[k] == ' ' || content[k] == '\t') {
			k++
		}
		return itemStart, k
	}
	return itemStart, itemEnd
}

// statementBounds returns the [start, end) byte offsets the statement's source
// text occupies — from the `import` keyword to the closing `}` (brace form) or
// the end of the last path / alias token (module form).
func statementBounds(content string, lineOffs []int, stmt *ast.ImportStmt) (int, int) {
	start := posToOffset(lineOffs, stmt.Line, stmt.Col)
	end := start
	consider := func(n ast.Node) {
		if e, ok := importNodeEnd(lineOffs, n); ok && e > end {
			end = e
		}
	}
	for _, n := range stmt.ModulePath {
		consider(n)
	}
	for _, n := range stmt.Names {
		consider(n)
	}
	for _, n := range stmt.Aliases {
		consider(n)
	}
	for _, n := range stmt.ExportAliases {
		consider(n)
	}
	consider(stmt.ModuleAlias)
	consider(stmt.ExportAlias)
	if stmt.IncludeParent && stmt.SelfLine > 0 {
		if e := posToOffset(lineOffs, stmt.SelfLine, stmt.SelfCol) + len("self"); e > end {
			end = e
		}
	}
	// Brace statements run to the closing `}`, which sits after the last item.
	// Colon single-name imports (`import std/io`) also have Names, but no
	// brace; do not scan past the line looking for an unrelated `}`.
	if len(stmt.Names) > 0 && end <= len(content) {
		lineEnd := len(content)
		if li := lineIndexOf(lineOffs, end); li+1 < len(lineOffs) {
			lineEnd = lineOffs[li+1]
		}
		if idx := strings.IndexByte(content[end:lineEnd], '}'); idx >= 0 {
			end += idx + 1
		}
	}
	return start, end
}

func nodeAt(nodes []ast.Node, idx int) []ast.Node {
	out := make([]ast.Node, 1)
	if idx >= 0 && idx < len(nodes) {
		out[0] = nodes[idx]
	}
	return out
}

func boolAt(flags []bool, idx int) []bool {
	out := make([]bool, 1)
	if idx >= 0 && idx < len(flags) {
		out[0] = flags[idx]
	}
	return out
}

// --- add import -------------------------------------------------------------

// buildAddImportActions returns an "Add import <spec>" quick-fix for each stdlib
// import the file could add (analysis.FindMissingImports) — for example
// `std/io` for a module qualifier, or `std/sets.Set` for a type. Each is matched to
// its reporting diagnostic by position (the same position gate the remove fix
// uses, and for the same glsp lossy-Code reason). When a type name is exported by
// more than one module, every candidate is offered; dedup is by import spec, so
// two `io.x` uses don't yield two identical fixes but two different modules do.
func buildAddImportActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	if len(diags) == 0 {
		return nil
	}
	missing := analysis.FindMissingImports(fa, nodes)
	if len(missing) == 0 {
		return nil
	}
	lines := newLineIndex(content)
	lineOffs := lines.starts
	added := make(map[string]bool)
	var actions []protocol.CodeAction
	for _, diag := range diags {
		for _, m := range matchAllMissing(lines, missing, diag) {
			if added[m.ImportSpec()] {
				continue
			}
			edit, ok := addImportEdit(content, lineOffs, nodes, m)
			if !ok {
				continue
			}
			added[m.ImportSpec()] = true
			kind := protocol.CodeActionKindQuickFix
			actions = append(actions, protocol.CodeAction{
				Title:       "Add import " + m.ImportSpec(),
				Kind:        &kind,
				Diagnostics: []protocol.Diagnostic{diag},
				Edit: &protocol.WorkspaceEdit{
					Changes: map[protocol.DocumentUri][]protocol.TextEdit{
						protocol.DocumentUri(uri): {edit},
					},
				},
			})
		}
	}
	return actions
}

// matchAllMissing returns every MissingImport whose 1-based position lies in
// the diagnostic's range — more than one when a type name is ambiguous
// across modules.
func matchAllMissing(lines *lineIndex, missing []analysis.MissingImport, diag protocol.Diagnostic) []analysis.MissingImport {
	var out []analysis.MissingImport
	for _, m := range missing {
		if diagCovers(lines, diag, m.Pos.Line, m.Pos.Col) {
			out = append(out, m)
		}
	}
	return out
}

// addImportEdit computes the TextEdit that makes m's module importable: either
// merging `self` into an existing selective import of the same module, or
// inserting a new `import <path>` statement. Both are rendered through
// format.RenderImports so the output matches `nomi fmt`. Both edits are
// localized (a single-statement replace or a single-line insert), so neither
// can clobber comments elsewhere in the import region.
func addImportEdit(content string, lineOffs []int, nodes []ast.Node, m analysis.MissingImport) (protocol.TextEdit, bool) {
	segs := strings.Split(m.ModulePath, "/")
	for _, stmt := range lspTopLevelImportStmts(nodes) {
		if !importStmtPathMatches(stmt, segs) {
			continue
		}
		merged, ok := mergeInto(stmt, m)
		if !ok {
			return protocol.TextEdit{}, false
		}
		// statementBounds starts at the module path (after `import ` for a
		// top-level statement; after the indent for a block entry), so we
		// replace it with the keyword-less body — correct for both forms.
		start, end := statementBounds(content, lineOffs, stmt)
		return rangeEdit(content, start, end, format.RenderImportBody(merged)), true
	}
	// No existing import of the module. Preserve the file's grouping style: when
	// the imports live in a block, append a new entry to it (which `nomi fmt`
	// then sorts) rather than a flat statement the formatter would never merge
	// into the block — and which a later fixAll couldn't clean, since the module
	// is no longer missing.
	if block := firstNonSynthImportBlock(nodes); block != nil {
		return blockEntryInsertEdit(content, lineOffs, block, m.ImportSpec()), true
	}
	return importInsertEdit(content, lineOffs, nodes, "import "+m.ImportSpec()), true
}

// mergeInto folds m into an existing same-module import statement, returning the
// merged copy (the original is never mutated). The missing import is an
// explicitly exported owner, so merging means adding that owner to the selector
// list. ok is false when m is already satisfied by stmt (defensive — the name
// would then have resolved).
func mergeInto(stmt *ast.ImportStmt, m analysis.MissingImport) (*ast.ImportStmt, bool) {
	if m.Member == "" {
		if len(stmt.Names) == 0 || stmt.IncludeParent {
			return nil, false
		}
		return cloneImportStmtWithSelf(stmt), true
	}
	if importStmtHasName(stmt, m.Member) {
		return nil, false
	}
	c := cloneImportStmtAddName(stmt, m.Member)
	if len(stmt.Names) == 0 && !stmt.IncludeParent {
		// Internal/recovered empty-selector import: keep that binding via self.
		c.IncludeParent = true
	}
	return c, true
}

// importStmtHasName reports whether stmt's brace list already binds name.
func importStmtHasName(stmt *ast.ImportStmt, name string) bool {
	for i, n := range stmt.Names {
		bound := ast.ImportNodeName(n)
		if i < len(stmt.Aliases) && stmt.Aliases[i] != nil {
			bound = ast.ImportNodeName(stmt.Aliases[i])
		}
		if bound == name {
			return true
		}
	}
	return false
}

// cloneImportStmtAddName returns a copy of stmt with name appended to its brace
// list, parallel slices kept aligned so the formatter's in-place selective sort
// stays safe.
func cloneImportStmtAddName(stmt *ast.ImportStmt, name string) *ast.ImportStmt {
	c := *stmt
	names := append(append([]ast.Node(nil), stmt.Names...), &ast.TypeIdent{Name: name})
	c.Names = names
	c.Aliases = padNodes(stmt.Aliases, len(names))
	c.ExportAliases = padNodes(stmt.ExportAliases, len(names))
	c.ExportFlags = padBools(stmt.ExportFlags, len(names))
	return &c
}

func padNodes(s []ast.Node, n int) []ast.Node {
	out := make([]ast.Node, n)
	copy(out, s)
	return out
}

func padBools(s []bool, n int) []bool {
	out := make([]bool, n)
	copy(out, s)
	return out
}

// firstNonSynthImportBlock returns the first non-synthesized ImportBlock among
// nodes, or nil.
func firstNonSynthImportBlock(nodes []ast.Node) *ast.ImportBlock {
	for _, n := range nodes {
		if b, ok := n.(*ast.ImportBlock); ok && !analysis.IsSynthesizedLine(b.Line) {
			return b
		}
	}
	return nil
}

// blockEntryInsertEdit inserts a new `import { ... }` entry line just before
// the block's closing `}`, indented as the block's existing entries are, or
// by the formatter's indent when it has none. The entry lands at the end
// unsorted; `nomi fmt` / organize sort it into place, which is the point — a
// block entry is fmt-mergeable where a flat statement after the block is not.
func blockEntryInsertEdit(content string, lineOffs []int, block *ast.ImportBlock, modulePath string) protocol.TextEdit {
	closeOff := matchBraceEnd(content, posToOffset(lineOffs, block.Line, block.Col))
	braceLine := lineIndexOf(lineOffs, closeOff)
	insertAt := lineOffs[braceLine]
	indent := strings.Repeat(" ", format.IndentWidth)
	if len(block.Entries) > 0 && block.Entries[0].Col > 1 {
		indent = strings.Repeat(" ", block.Entries[0].Col-1)
	}
	return rangeEdit(content, insertAt, insertAt, indent+modulePath+"\n")
}

// importInsertEdit places a new import statement on its own line: after the
// last existing import construct, or — when the file has none — at the top with
// a blank line separating it from the code that follows.
func importInsertEdit(content string, lineOffs []int, nodes []ast.Node, text string) protocol.TextEdit {
	endLine := lastImportLine(content, lineOffs, nodes)
	if endLine < 0 {
		return rangeEdit(content, 0, 0, text+"\n\n")
	}
	if endLine+1 < len(lineOffs) {
		off := lineOffs[endLine+1]
		return rangeEdit(content, off, off, text+"\n")
	}
	// Last import is on the file's final line with no trailing newline.
	return rangeEdit(content, len(content), len(content), "\n"+text+"\n")
}

// lastImportLine returns the 0-based index of the last line occupied by any
// top-level import construct (statement or block), or -1 when there are none.
func lastImportLine(content string, lineOffs []int, nodes []ast.Node) int {
	endLine := -1
	for _, node := range nodes {
		switch v := node.(type) {
		case *ast.ImportStmt:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			_, e := statementBounds(content, lineOffs, v)
			if l := lineIndexOf(lineOffs, e-1); l > endLine {
				endLine = l
			}
		case *ast.ImportBlock:
			if analysis.IsSynthesizedLine(v.Line) {
				continue
			}
			startOff := posToOffset(lineOffs, v.Line, v.Col)
			closeOff := matchBraceEnd(content, startOff)
			if l := lineIndexOf(lineOffs, closeOff); l > endLine {
				endLine = l
			}
		}
	}
	return endLine
}

// matchBraceEnd returns the offset of the `}` that closes the first `{` at or
// after startOff, honoring nesting (an import block's entries may carry their
// own `{...}` selective lists). Falls back to the last byte if unbalanced.
func matchBraceEnd(content string, startOff int) int {
	depth := 0
	seen := false
	for i := startOff; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
			seen = true
		case '}':
			depth--
			if seen && depth == 0 {
				return i
			}
		}
	}
	return len(content) - 1
}

// cloneImportStmtWithSelf returns a copy of stmt with IncludeParent set, with
// its parallel slices copied so rendering (which sorts the selective list in
// place) can't mutate the cached AST node.
func cloneImportStmtWithSelf(stmt *ast.ImportStmt) *ast.ImportStmt {
	c := *stmt
	c.IncludeParent = true
	c.Names = append([]ast.Node(nil), stmt.Names...)
	c.Aliases = append([]ast.Node(nil), stmt.Aliases...)
	c.ExportFlags = append([]bool(nil), stmt.ExportFlags...)
	c.ExportAliases = append([]ast.Node(nil), stmt.ExportAliases...)
	return &c
}

// importStmtPathMatches reports whether stmt's module path equals segs
// segment-for-segment (e.g. ["std", "io"]).
func importStmtPathMatches(stmt *ast.ImportStmt, segs []string) bool {
	if len(stmt.ModulePath) != len(segs) {
		return false
	}
	for i, n := range stmt.ModulePath {
		if ast.ImportNodeName(n) != segs[i] {
			return false
		}
	}
	return true
}

// identPath builds a module path of *ast.Ident segments from string segments.
func identPath(segs []string) []ast.Node {
	out := make([]ast.Node, len(segs))
	for i, s := range segs {
		out[i] = &ast.Ident{Name: s}
	}
	return out
}

// lspTopLevelImportStmts flattens the file's top-level ImportStmt / ImportBlock
// entries, skipping synthesized (prelude-chain) statements.
func lspTopLevelImportStmts(nodes []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	add := func(n *ast.ImportStmt) {
		if n == nil || analysis.IsSynthesizedLine(n.Line) {
			return
		}
		out = append(out, n)
	}
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.ImportStmt:
			add(n)
		case *ast.ImportBlock:
			for _, e := range n.Entries {
				add(e)
			}
		}
	}
	return out
}

// --- offset / position helpers ---------------------------------------------

// lineOffsets returns the byte offset of the start of each 0-based line.
func lineOffsets(content string) []int {
	offs := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

// posToOffset converts a 1-based (line, col) into a byte offset.
func posToOffset(lineOffs []int, line, col int) int {
	idx := line - 1
	if idx < 0 || idx >= len(lineOffs) {
		return 0
	}
	return lineOffs[idx] + (col - 1)
}

// lineIndexOf returns the 0-based line index containing the given byte offset.
func lineIndexOf(lineOffs []int, offset int) int {
	// The first line starting after offset, less one.
	return max(sort.SearchInts(lineOffs, offset+1)-1, 0)
}

// offsetToPosition converts a byte offset into a 0-based protocol.Position.
func offsetToPosition(content string, offset int) protocol.Position {
	line, col := 0, 0
	for i := 0; i < offset && i < len(content); i++ {
		if content[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return protocol.Position{Line: uint32(line), Character: uint32(col)}
}

func rangeEdit(content string, startOff, endOff int, newText string) protocol.TextEdit {
	return protocol.TextEdit{
		Range: protocol.Range{
			Start: offsetToPosition(content, startOff),
			End:   offsetToPosition(content, endOff),
		},
		NewText: newText,
	}
}

func importNodeStart(lineOffs []int, n ast.Node) (int, bool) {
	switch v := n.(type) {
	case *ast.Ident:
		return posToOffset(lineOffs, v.Line, v.Col), true
	case *ast.TypeIdent:
		return posToOffset(lineOffs, v.Line, v.Col), true
	}
	return 0, false
}

func importNodeEnd(lineOffs []int, n ast.Node) (int, bool) {
	start, ok := importNodeStart(lineOffs, n)
	if !ok {
		return 0, false
	}
	return start + len(ast.ImportNodeName(n)), true
}
