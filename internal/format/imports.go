package format

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// combineImports gathers each comment-delimited run of top-level imports into a
// canonical `import { … }` block. Bare blank lines between imports are
// formatting noise and collapse; a comment between imports keeps that boundary
// visible. Same-module selective statements merge; entries that cannot be
// merged losslessly pass through unchanged. A single-construct group is left
// as-is (a lone import stays bare; a lone block stays a block — its single
// entry is collapsed to bare at emit time).
//
// Runs before sortImports (which then sorts each block's entries) in Format.
func combineImports(nodes []ast.Node) []ast.Node {
	end := importRunEnd(nodes)
	if end == 0 {
		return nodes
	}
	var out []ast.Node
	groupStart := 0
	for i := 1; i < end; i++ {
		if hasLeadingComment(nodes[i]) || importNodeGo(nodes[i]) != importNodeGo(nodes[groupStart]) {
			out = append(out, combineGroup(nodes[groupStart:i]))
			groupStart = i
		}
	}
	out = append(out, combineGroup(nodes[groupStart:end]))
	return append(out, nodes[end:]...)
}

func normalizeImportLayout(nodes []ast.Node) []ast.Node {
	nodes = combineImports(nodes)
	nodes = sortImports(nodes)
	for _, node := range nodes {
		normalizeNestedImportLayout(node)
	}
	return nodes
}

func normalizeNestedImportLayout(node ast.Node) {
	switch n := node.(type) {
	case *ast.FuncDef:
		normalizeBlockImportLayout(n.Body)
	case *ast.TestDecl:
		normalizeNestedImportLayout(n.Boot)
		normalizeNestedImportLayout(n.Setup)
		normalizeBlockImportLayout(n.Body)
	case *ast.Block:
		normalizeBlockImportLayout(n)
	case *ast.If:
		normalizeNestedImportLayout(n.CondPattern)
		normalizeBlockImportLayout(n.Then)
		normalizeNestedImportLayout(n.Else)
	case *ast.Case:
		normalizeNestedImportLayout(n.Value)
		for i := range n.Branches {
			normalizeNestedImportLayout(n.Branches[i].Pattern)
			normalizeNestedImportLayout(n.Branches[i].Guard)
			normalizeNestedImportLayout(n.Branches[i].Body)
		}
	case *ast.Lambda:
		normalizeBlockImportLayout(n.Body)
	case *ast.ConcurrentBlock:
		normalizeBlockImportLayout(n.Body)
	case *ast.Binding:
		normalizeNestedImportLayout(n.Value)
	case *ast.PatternDestructure:
		normalizeNestedImportLayout(n.Value)
	case *ast.PatternBinding:
		normalizeNestedImportLayout(n.Value)
		for _, e := range n.ElseNodes() {
			normalizeNestedImportLayout(e)
		}
	case *ast.ExprStmt:
		normalizeNestedImportLayout(n.Expr)
	case *ast.Return:
		normalizeNestedImportLayout(n.Value)
	case *ast.TryOp:
		if n.Expr != nil {
			normalizeNestedImportLayout(n.Expr)
		}
	case *ast.Dbg:
		normalizeNestedImportLayout(n.Expr)
	case *ast.Then:
		normalizeNestedImportLayout(n.Lambda)
	case *ast.Assertion:
		normalizeNestedImportLayout(n.Expr)
	case *ast.With:
		normalizeNestedImportLayout(n.Value)
	case *ast.Defer:
		normalizeNestedImportLayout(n.Call)
	case *ast.GroupedExpr:
		normalizeNestedImportLayout(n.Expr)
	case *ast.Unary:
		normalizeNestedImportLayout(n.Right)
	case *ast.Binary:
		normalizeNestedImportLayout(n.Left)
		normalizeNestedImportLayout(n.Right)
	case *ast.Call:
		normalizeNestedImportLayout(n.Func)
		for _, arg := range n.Args {
			normalizeNestedImportLayout(arg)
		}
	case *ast.NamedArg:
		normalizeNestedImportLayout(n.Value)
	case *ast.FieldAccess:
		normalizeNestedImportLayout(n.Object)
	case *ast.StructLit:
		for i := range n.Fields {
			normalizeNestedImportLayout(n.Fields[i].Value)
		}
	case *ast.ListLit:
		for _, elem := range n.Items {
			normalizeNestedImportLayout(elem)
		}
	case *ast.VectorLit:
		for _, elem := range n.Items {
			normalizeNestedImportLayout(elem)
		}
	case *ast.SetLit:
		for _, elem := range n.Items {
			normalizeNestedImportLayout(elem)
		}
	case *ast.ListSpreadLit:
		for _, elem := range n.Heads {
			normalizeNestedImportLayout(elem)
		}
		normalizeNestedImportLayout(n.TailSpread)
	case *ast.TupleLit:
		for _, elem := range n.Items {
			normalizeNestedImportLayout(elem)
		}
	case *ast.MapLit:
		for _, entry := range n.Entries {
			normalizeNestedImportLayout(entry.Key)
			normalizeNestedImportLayout(entry.Value)
		}
	}
}

func normalizeBlockImportLayout(block *ast.Block) {
	if block == nil {
		return
	}
	block.Stmts = moveImportsToBlockTop(block.Stmts)
	block.Stmts = combineImports(block.Stmts)
	block.Stmts = sortImports(block.Stmts)
	for _, stmt := range block.Stmts {
		normalizeNestedImportLayout(stmt)
	}
}

func moveImportsToBlockTop(stmts []ast.Node) []ast.Node {
	var imports []ast.Node
	var body []ast.Node
	for _, stmt := range stmts {
		if isImportNode(stmt) {
			imports = append(imports, stmt)
		} else {
			body = append(body, stmt)
		}
	}
	if len(imports) == 0 || len(body) == 0 {
		return stmts
	}
	stripLeadingBlank(imports[0])
	out := make([]ast.Node, 0, len(stmts))
	out = append(out, imports...)
	out = append(out, body...)
	return out
}

func stripLeadingBlank(node ast.Node) {
	switch n := node.(type) {
	case *ast.ImportStmt:
		n.Leading = trimLeadingBlankTrivia(n.Leading)
	case *ast.ImportBlock:
		n.Leading = trimLeadingBlankTrivia(n.Leading)
	}
}

func trimLeadingBlankTrivia(trivia []ast.Trivia) []ast.Trivia {
	for len(trivia) > 0 && trivia[0].Kind == ast.TriviaBlankLine {
		trivia = trivia[1:]
	}
	return trivia
}

// combineGroup combines one import section into a single block, or returns the
// lone construct unchanged for a one-element group.
func combineGroup(group []ast.Node) ast.Node {
	if len(group) == 1 {
		setImportLeading(group[0], trimBareLeadingBlankTrivia(importLeading(group[0])))
		return group[0]
	}
	goBlock := importNodeGo(group[0])
	var entries []*ast.ImportStmt
	var endTrivia []ast.Trivia
	for i, n := range group {
		switch v := n.(type) {
		case *ast.ImportStmt:
			if i > 0 {
				v.Leading = trimBareLeadingBlankTrivia(v.Leading)
			}
			entries = append(entries, v)
		case *ast.ImportBlock:
			if i > 0 && len(v.Entries) > 0 {
				// A non-first block's own leading comment moves onto its first
				// entry. Bare separating blank lines are dropped so imports
				// collapse into one sorted section.
				v.Entries[0].Leading = append(append([]ast.Trivia(nil), trimBareLeadingBlankTrivia(v.Leading)...), v.Entries[0].Leading...)
			}
			entries = append(entries, v.Entries...)
			endTrivia = append(endTrivia, v.EndTrivia...)
		}
	}
	entries = mergeSameModule(entries)
	block := &ast.ImportBlock{Entries: entries, Go: goBlock, EndTrivia: endTrivia, Line: group[0].LineNum()}
	// The group head's leading (usually a section/header comment) stays above
	// the block. If the head was a flat statement it is also entries[0]; clear
	// its copy so the comment isn't emitted twice.
	if h, ok := group[0].(ast.HasTrivia); ok {
		block.Leading = trimBareLeadingBlankTrivia(h.GetLeading())
	}
	if first, ok := group[0].(*ast.ImportStmt); ok {
		first.Leading = nil
	}
	return block
}

func importNodeGo(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.ImportStmt:
		return v.Extern
	case *ast.ImportBlock:
		return v.Go
	default:
		return false
	}
}

func trimBareLeadingBlankTrivia(trivia []ast.Trivia) []ast.Trivia {
	for _, t := range trivia {
		if t.Kind == ast.TriviaComment {
			return trivia
		}
	}
	return trimLeadingBlankTrivia(trivia)
}

// importRunEnd returns the length of the leading run of import constructs.
func importRunEnd(nodes []ast.Node) int {
	end := 0
	for end < len(nodes) {
		switch nodes[end].(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			end++
		default:
			return end
		}
	}
	return end
}

// mergeSameModule merges entries that share a module path. Entries with legacy
// module aliases or line-level export shorthand are left separate because their
// bindings/modifiers must be preserved exactly. Order follows first occurrence.
func mergeSameModule(entries []*ast.ImportStmt) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	idx := map[string]int{}
	for _, e := range entries {
		if e.Extern || e.ModuleAlias != nil || e.ExportAll {
			out = append(out, e)
			continue
		}
		key := modulePathKeyStmt(e)
		if j, ok := idx[key]; ok {
			out[j] = mergeImportPair(out[j], e)
		} else {
			idx[key] = len(out)
			out = append(out, e)
		}
	}
	return out
}

func modulePathKeyStmt(stmt *ast.ImportStmt) string {
	parts := make([]string, len(stmt.ModulePath))
	for i, n := range stmt.ModulePath {
		parts[i] = ast.ImportNodeName(n)
	}
	return strings.Join(parts, "/")
}

// mergeImportPair merges b into a (same module path), returning a fresh
// statement. Names union (dedup by bound name, aliases + export flags carried);
// `self` is set when the result has names and either input bound the module
// (bare import or an existing `self`); comments concatenate in a→b order.
func mergeImportPair(a, b *ast.ImportStmt) *ast.ImportStmt {
	var names, aliases, exportAliases []ast.Node
	var flags []bool
	seen := map[string]bool{}
	add := func(s *ast.ImportStmt) {
		for i, n := range s.Names {
			bound := ast.ImportNodeName(n)
			var alias ast.Node
			if i < len(s.Aliases) && s.Aliases[i] != nil {
				alias = s.Aliases[i]
				bound = ast.ImportNodeName(alias)
			}
			if seen[bound] {
				continue
			}
			seen[bound] = true
			names = append(names, n)
			aliases = append(aliases, alias)
			flags = append(flags, i < len(s.ExportFlags) && s.ExportFlags[i])
			var exAlias ast.Node
			if i < len(s.ExportAliases) {
				exAlias = s.ExportAliases[i]
			}
			exportAliases = append(exportAliases, exAlias)
		}
	}
	add(a)
	add(b)
	bareOrSelf := func(s *ast.ImportStmt) bool { return s.IncludeParent || len(s.Names) == 0 }
	merged := *a
	merged.Names = names
	merged.Aliases = aliases
	merged.ExportFlags = flags
	merged.ExportAliases = exportAliases
	merged.IncludeParent = len(names) > 0 && (bareOrSelf(a) || bareOrSelf(b))
	merged.Leading = append(append([]ast.Trivia(nil), a.Leading...), b.Leading...)
	merged.Trailing = append(append([]ast.Trivia(nil), a.Trailing...), b.Trailing...)
	return &merged
}

// sortImports rearranges consecutive import statements at the top of the file.
// Non-import statements (anything other than *ast.ImportStmt or *ast.ImportBlock)
// break the sort — once one is seen, the remaining imports (if any) are left
// alone to avoid reordering imports intentionally placed after code.
//
// Within the sorted run:
//
//   - The run is split into comment-delimited sections. Bare blank lines do not
//     create sections.
//   - Each group is ordered by origin (std/* first, everything else second) —
//     stable-sorted so in-group order comes from the alphabetical comparator.
//   - Alphabetical order is by module path joined with `/`; imported-name
//     aliases do not affect the path sort.
//   - Selective lists inside `{...}` are sorted alphabetically by the
//     original imported name (pre-alias).
//   - Block-form imports get their inner entries sorted with the same
//     group-then-alphabetical rule. A block keeps its position in the run;
//     only per-statement imports reorder.
//   - A group's first node carries the group header trivia (a section comment
//     and the blank-line separator). That trivia is positional — it stays at
//     the group head even when sorting moves the node that originally carried
//     it (see hoistGroupHeader).
func sortImports(nodes []ast.Node) []ast.Node {
	// Find the run of import-shaped nodes at the top (ImportStmt or
	// ImportBlock).
	end := 0
	for end < len(nodes) {
		switch nodes[end].(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			end++
			continue
		}
		break
	}
	if end == 0 {
		return nodes
	}

	// Sort the inner entries of any block-form imports. A block is
	// self-contained — its entries sort independent of the surrounding run.
	for i := 0; i < end; i++ {
		if blk, ok := nodes[i].(*ast.ImportBlock); ok {
			// Selective lists sort regardless of entry count (a single-entry
			// block — e.g. a same-module merge result — must still have its
			// `{...}` sorted, or it would only canonicalize on the second
			// format pass after collapsing to bare). Entry ORDER only needs
			// sorting when there's more than one.
			if len(blk.Entries) > 1 {
				sortImportStmts(blk.Entries)
			}
			if len(blk.Entries) > 0 {
				blk.Entries[0].Leading = trimLeadingBlankTrivia(blk.Entries[0].Leading)
			}
			for _, entry := range blk.Entries {
				sortSelectiveList(entry)
			}
		}
	}
	// Sort selective lists on top-level per-statement imports too.
	for i := 0; i < end; i++ {
		if stmt, ok := nodes[i].(*ast.ImportStmt); ok {
			sortSelectiveList(stmt)
		}
	}

	// Split [0, end) into blank-line-delimited groups and sort each group
	// in place. A node whose leading trivia begins with a blank line starts
	// a new group.
	groupStart := 0
	for i := 1; i < end; i++ {
		if hasLeadingBlank(nodes[i]) {
			sortImportGroup(nodes[groupStart:i])
			groupStart = i
		}
	}
	sortImportGroup(nodes[groupStart:end])

	return nodes
}

// sortImportGroup sorts the per-statement imports within one blank-line group
// in place. ImportBlock nodes keep their slots; only *ast.ImportStmt nodes
// reorder. The group's header trivia (on the first node) is hoisted to stay
// at the head — see hoistGroupHeader.
func sortImportGroup(group []ast.Node) {
	if len(group) <= 1 {
		return
	}
	head := importLeading(group[0])
	setImportLeading(group[0], nil)

	stmtPos := make([]int, 0, len(group))
	stmts := make([]*ast.ImportStmt, 0, len(group))
	for i, n := range group {
		if s, ok := n.(*ast.ImportStmt); ok {
			stmtPos = append(stmtPos, i)
			stmts = append(stmts, s)
		}
	}
	sortImportStmts(stmts)
	for k, pos := range stmtPos {
		group[pos] = stmts[k]
	}

	hoistGroupHeader(group, head)
}

// hoistGroupHeader re-attaches the detached group-header trivia (a section
// comment plus the blank-line separator that precedes the group) to whatever
// node is now first, above any trivia that node already carried. Treating the
// first node's leading trivia as positional keeps a file/section header on top
// even when sorting reorders the imports beneath it.
func hoistGroupHeader(group []ast.Node, head []ast.Trivia) {
	if len(head) == 0 {
		return
	}
	first := group[0]
	merged := append(append([]ast.Trivia(nil), head...), importLeading(first)...)
	setImportLeading(first, merged)
}

// importLeading returns an import node's leading trivia (nil for non-import
// nodes).
func importLeading(n ast.Node) []ast.Trivia {
	if ht, ok := n.(ast.HasTrivia); ok {
		return ht.GetLeading()
	}
	return nil
}

// setImportLeading replaces an import node's leading trivia.
func setImportLeading(n ast.Node, tr []ast.Trivia) {
	switch v := n.(type) {
	case *ast.ImportStmt:
		v.Leading = tr
	case *ast.ImportBlock:
		v.Leading = tr
	}
}

// sortImportStmts sorts a slice of *ast.ImportStmt in-place by group
// (std.* first) then alphabetical module path.
func sortImportStmts(stmts []*ast.ImportStmt) {
	sort.SliceStable(stmts, func(i, j int) bool {
		gi, gj := importGroup(stmts[i]), importGroup(stmts[j])
		if gi != gj {
			return gi < gj
		}
		return importPathString(stmts[i]) < importPathString(stmts[j])
	})
}

// sortSelectiveList sorts the selective Names plus the parallel Aliases,
// ExportFlags, and ExportAliases of an import statement alphabetically by
// the original (pre-alias) name. All four parallel slices must reorder
// together so the per-item re-export flags stay attached to their names.
func sortSelectiveList(imp *ast.ImportStmt) {
	if len(imp.Names) <= 1 {
		return
	}
	idx := make([]int, len(imp.Names))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return ast.ImportNodeName(imp.Names[idx[i]]) <
			ast.ImportNodeName(imp.Names[idx[j]])
	})
	newNames := make([]ast.Node, len(imp.Names))
	newAliases := make([]ast.Node, len(imp.Aliases))
	var newExportFlags []bool
	if len(imp.ExportFlags) > 0 {
		newExportFlags = make([]bool, len(imp.ExportFlags))
	}
	var newExportAliases []ast.Node
	if len(imp.ExportAliases) > 0 {
		newExportAliases = make([]ast.Node, len(imp.ExportAliases))
	}
	for newPos, oldPos := range idx {
		newNames[newPos] = imp.Names[oldPos]
		if oldPos < len(imp.Aliases) {
			newAliases[newPos] = imp.Aliases[oldPos]
		}
		if oldPos < len(imp.ExportFlags) {
			newExportFlags[newPos] = imp.ExportFlags[oldPos]
		}
		if oldPos < len(imp.ExportAliases) {
			newExportAliases[newPos] = imp.ExportAliases[oldPos]
		}
	}
	imp.Names = newNames
	imp.Aliases = newAliases
	if newExportFlags != nil {
		imp.ExportFlags = newExportFlags
	}
	if newExportAliases != nil {
		imp.ExportAliases = newExportAliases
	}
}

// importGroup returns the origin-group index for grouping imports. std.* is
// group 0; everything else is group 1 (local imports, placeholder until we
// have a richer notion of origin).
func importGroup(imp *ast.ImportStmt) int {
	if imp.Extern {
		return 1
	}
	if len(imp.ModulePath) > 0 && ast.ImportNodeName(imp.ModulePath[0]) == "std" {
		return 0
	}
	return 1
}

// importPathString joins the slash-separated module path into a single string
// for alphabetical comparison. Module-level aliases (`as alias`) are
// intentionally ignored — we sort by where the module lives, not what the
// caller renamed it to.
func importPathString(imp *ast.ImportStmt) string {
	if imp.Extern {
		return imp.ExternPath
	}
	parts := make([]string, 0, len(imp.ModulePath))
	for _, p := range imp.ModulePath {
		parts = append(parts, ast.ImportNodeName(p))
	}
	return strings.Join(parts, "/")
}
