package analysis

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckUnusedImports reports a TypeError for every imported item that no
// visible text in the file uses. Granularity is per imported ITEM:
//
//   - a selective brace item (`import std/comparable.{Comparable, Ordering}`
//     with `Ordering` dead) errors on that name at its own position;
//   - an aliased item is used through its alias (the alias IS the binding);
//   - `self` in a brace list is its own item, used iff the parent
//     module/enum name it binds is referenced.
//
// Re-exports are used by definition: `import path.{a export}` and line-level
// `export` on a selective list exempt the re-exported items (the prelude is
// the canonical consumer). Synthesized imports (the auto-prepended prelude
// chain — synth-band positions) are skipped entirely: they are compiler output,
// not visible text.
//
// WHERE THIS RUNS — end of the BUILDER phase, before CheckTypes:
//
//   - single-file analysis: the tail of (*builder).buildModule, so
//     BuildFileWithStdlib / BuildFile callers (LSP raw documents, on-demand
//     resolveImport module loads, white-box tests) get the diagnostic with
//     no extra wiring;
//   - project builds: a dedicated sweep in buildProjectWithCache right after
//     Sweep B-bodies (the pass that records expression references) — see
//     the call site there.
//
// Diagnostics are produced at that builder boundary because everything that
// counts as a use — expressions, patterns, type annotations, impl headers,
// interface bounds, `@derive` args, typed-literal tags — has been recorded by
// then, before later passes add unrelated diagnostics. The structured query
// (`FindUnusedImports`) is also used later by LSP code actions, after
// CheckTypes may have rewritten some References entries with per-call-site
// proxy copies. For that reason it treats either the original Symbol pointer
// OR the copied import-binding source position as a use.
//
// The analyzer-less REPL never runs the builder pipeline and is exempt by
// construction.
//
// An unused import of a whole project file is not reported here. It is kept
// in fa.ImplImports, because importing a file is also what puts the file's
// impl blocks into the program, and whether the program uses one of them is
// known only after every file is type-checked. CheckImplImports decides it.
func CheckUnusedImports(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	fa.ImplImports = nil
	unused := findUnusedImports(fa, nodes)
	if len(unused) == 0 {
		return nil
	}
	errs := make([]TypeError, 0, len(unused))
	for _, u := range unused {
		if u.module != nil && !isStdlibImportStmt(fa, u.Stmt) {
			fa.ImplImports = append(fa.ImplImports, &ImplImport{UnusedImport: u})
			continue
		}
		errs = append(errs, unusedImportError(u))
	}
	return errs
}

func unusedImportError(u UnusedImport) TypeError {
	return TypeError{
		Line:    u.Pos.Line,
		Col:     u.Pos.Col,
		Message: u.Message,
		Code:    UnusedImportCode,
	}
}

// isStdlibImportStmt reports whether n imports a standard-library file:
// `std/...` anywhere, or a bare path inside a stdlib file. Every stdlib file's
// impls are in every program whether it is imported or not, so an import of
// one is never needed for them.
func isStdlibImportStmt(fa *FileAnalysis, n *ast.ImportStmt) bool {
	if len(n.ModulePath) == 0 {
		return false
	}
	path := make([]string, len(n.ModulePath))
	for i, seg := range n.ModulePath {
		path[i] = ast.ImportNodeName(seg)
	}
	return QualifyIntraStdlibImport(fa.Origin, path)[0] == "std"
}

// UnusedImportCode tags every unused-import TypeError / LSP diagnostic so the
// code-action handler can recognize the class without sniffing message text.
const UnusedImportCode = "unused-import"

// UnusedItemKind classifies one unused imported item by the surgical edit its
// removal requires.
type UnusedItemKind int

const (
	// UnusedWholeModule is retained for AST shapes synthesized internally or
	// recovered from invalid source. The whole statement is removed.
	UnusedWholeModule UnusedItemKind = iota
	// UnusedModuleAlias is retained for AST shapes recovered from invalid
	// source. The whole statement is removed.
	UnusedModuleAlias
	// UnusedBraceItem is one selective name in a `{...}` list.
	UnusedBraceItem
	// UnusedSelfMarker is the `self` item in a `{self, ...}` list.
	UnusedSelfMarker
)

// UnusedImport identifies one unused imported item for both diagnostics and
// fixes. Pos is the 1-based diagnostic position (the item as written);
// Message is the rendered diagnostic text.
type UnusedImport struct {
	Stmt     *ast.ImportStmt // owning statement
	Name     string          // bound name reported as unused
	ItemKind UnusedItemKind  // which surgical-removal shape applies
	NameIdx  int             // index into Stmt.Names for BraceItem; -1 otherwise
	Pos      Pos             // diagnostic position (1-based)
	Message  string          // rendered diagnostic message
	// module is the imported file's scope, for a whole-file import whose
	// file resolved. Nil for every other item.
	module *Scope
}

// FindUnusedImports returns one UnusedImport per imported item that no visible
// text in the file uses. This is the structured query both the unused-import
// diagnostic (via CheckUnusedImports) and the LSP remove-unused-import
// quick-fix consume. See CheckUnusedImports' doc for the granularity rules and
// the where-this-runs / usedness-by-pointer-identity caveats.
//
// A whole-file import in fa.ImplImports is included only once
// CheckImplImports has found that the program uses none of the file's impl
// blocks. Until then, removing it could drop impls the program needs.
func FindUnusedImports(fa *FileAnalysis, nodes []ast.Node) []UnusedImport {
	unused := findUnusedImports(fa, nodes)
	if len(fa.ImplImports) == 0 {
		return unused
	}
	keep := make(map[*ast.ImportStmt]bool, len(fa.ImplImports))
	for _, ii := range fa.ImplImports {
		if !ii.Unused {
			keep[ii.Stmt] = true
		}
	}
	var out []UnusedImport
	for _, u := range unused {
		if u.module != nil && keep[u.Stmt] {
			continue
		}
		out = append(out, u)
	}
	return out
}

func findUnusedImports(fa *FileAnalysis, nodes []ast.Node) []UnusedImport {
	stmts := topLevelImportStmts(nodes)
	if len(stmts) == 0 {
		return nil
	}

	// Positions the import statements themselves occupy. defineImport
	// registers References AT the import line (module-path segments, the
	// pre-alias original name, the drill-through enum segment) purely for
	// hover / go-to-def; those self-references must not count as uses.
	ownPos := make(map[Pos]bool)
	for _, n := range stmts {
		for _, seg := range n.ModulePath {
			markImportNodePos(ownPos, seg)
		}
		for _, name := range n.Names {
			markImportNodePos(ownPos, name)
		}
		for _, alias := range n.Aliases {
			markImportNodePos(ownPos, alias)
		}
		for _, ea := range n.ExportAliases {
			markImportNodePos(ownPos, ea)
		}
		markImportNodePos(ownPos, n.ModuleAlias)
		markImportNodePos(ownPos, n.ExportAlias)
		if n.IncludeParent && n.SelfLine > 0 {
			ownPos[Pos{Line: n.SelfLine, Col: n.SelfCol}] = true
		}
	}

	items := collectImportItems(fa, stmts)
	if len(items) == 0 {
		return nil
	}

	// Index the items for the single scan over fa.References.
	bySym := make(map[*Symbol]int, len(items))
	byBindingPos := make(map[importBindingUseKey]int, len(items))
	byModuleName := make(map[string]int)
	byVariantName := make(map[string]int)
	byRealNode := make(map[ast.Node]int)
	byQualifiedObjectName := make(map[string]int, len(items))
	for i, it := range items {
		bySym[it.sym] = i
		byBindingPos[importBindingUseKey{name: it.sym.Name, pos: it.sym.Pos}] = i
		byQualifiedObjectName[it.name] = i
		// The bound name as well as the token, for the same reason
		// `message` prefers it: a dotted import binds `Probe.Reading`, and
		// that whole name is what a qualifier in the file spells.
		if it.sym != nil && it.sym.Name != "" {
			byQualifiedObjectName[it.sym.Name] = i
		}
		if it.sym.Kind == SymbolModule {
			// Module-member access (`io.print`) records the module-object
			// reference via b.moduleSyms, which in some build shapes holds a
			// synthetic stand-in rather than this file's import symbol. A
			// SymbolModule reference under the bound name at a non-import
			// position can only arise from text using that name (module
			// access requires explicit visibility), so name-matching is
			// sound within one file.
			byModuleName[it.sym.Name] = i
		}
		if it.sym.Kind == SymbolEnumVariant {
			byVariantName[it.sym.Name] = i
		}
		// Typed-literal tags (`Date"..."`) record a fresh proxy Symbol whose
		// Node is the resolved type's declaration node (resolveTaggedStringTag)
		// — neither the import binding nor a copy of it. Match those uses
		// through the binding's resolved declaration node.
		if real := resolvedImportTarget(it.sym); real != nil && real.Node != nil {
			byRealNode[real.Node] = i
		}
	}

	used := make([]bool, len(items))
	for pos, ref := range fa.References {
		if ref == nil || ownPos[pos] {
			continue
		}
		// Synth-band positions are compiler output, not visible text. The
		// auto-prepended prelude chain registers its own import-line
		// references there (path-segment SymbolModule entries named "list",
		// "io", ... — see cloneImportStmtForInject), which would otherwise
		// satisfy the module-name rule below and mask every dead stdlib
		// module import in project builds.
		if IsSynthesizedLine(pos.Line) {
			continue
		}
		if i, ok := bySym[ref]; ok {
			used[i] = true
		}
		if i, ok := byBindingPos[importBindingUseKey{name: ref.Name, pos: ref.Pos}]; ok {
			used[i] = true
		}
		// A name reached by drilling counts as using the import it was
		// drilled through. `import telemetry.Probe` is what makes
		// `Probe.Reading` reachable, so naming `Probe.Reading` and nothing
		// else must not report `Probe` unused — the import is doing exactly
		// the job it was written for.
		if idx := strings.IndexByte(ref.Name, '.'); idx > 0 {
			if i, ok := byBindingPos[importBindingUseKey{name: ref.Name[:idx], pos: ref.Pos}]; ok {
				used[i] = true
			}
		}
		if ref.Resolved != nil {
			if i, ok := bySym[ref.Resolved]; ok {
				used[i] = true
			}
			if ref.Kind == SymbolFunction && ref.Resolved.Node != nil {
				if i, ok := byRealNode[ref.Resolved.Node]; ok {
					used[i] = true
				}
			}
		}
		if ref.Kind == SymbolModule {
			if i, ok := byModuleName[ref.Name]; ok {
				used[i] = true
			}
		}
		if ref.Kind == SymbolEnumVariant {
			if i, ok := byVariantName[ref.Name]; ok {
				used[i] = true
			}
		}
		if ref.Node != nil {
			if i, ok := byRealNode[ref.Node]; ok {
				used[i] = true
			}
		}
	}
	markQualifiedObjectUses(nodes, byQualifiedObjectName, used)

	var out []UnusedImport
	for i, it := range items {
		if used[i] {
			continue
		}
		u := UnusedImport{
			Stmt:     it.stmt,
			Name:     it.name,
			ItemKind: it.kind,
			NameIdx:  it.nameIdx,
			Pos:      it.pos,
			Message:  it.message(),
		}
		if it.module {
			u.module = it.sym.ModuleScope
		}
		out = append(out, u)
	}
	return out
}

type importBindingUseKey struct {
	name string
	pos  Pos
}

func markQualifiedObjectUses(nodes []ast.Node, byName map[string]int, used []bool) {
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if v.CanInterface() {
				if fa, ok := v.Interface().(*ast.FieldAccess); ok {
					switch obj := fa.Object.(type) {
					case *ast.Ident:
						if !IsSynthesizedLine(obj.Line) {
							if i, ok := byName[obj.Name]; ok {
								used[i] = true
							}
						}
					case *ast.TypeIdent:
						if !IsSynthesizedLine(obj.Line) {
							if i, ok := byName[obj.Name]; ok {
								used[i] = true
							}
						}
					}
					// A dotted name is the qualifier of `Probe.Reading.Steady`
					// whole, and the switch above only ever sees its leading
					// segment — which is not what the import bound, and on its
					// own is not a name the file has. Mark the dotted prefixes
					// too, so the import that made the qualifier reachable
					// counts as used.
					if fa.Field != nil && !IsSynthesizedLine(fa.Field.Line) {
						if path := fieldAccessPath(fa); len(path) >= 3 {
							for end := 2; end < len(path); end++ {
								if i, ok := byName[strings.Join(path[:end], ".")]; ok {
									used[i] = true
								}
							}
						}
					}
				}
				// A LITERAL-ATTACH HEAD IS NOT A FieldAccess. `shapes.Circle{r: 1}`
				// and `shapes.Shape.Ring{r: 2}` are parsed as a StructLit whose
				// TypeName is a QualifiedType holding the whole dotted prefix in
				// one string, so the FieldAccess arms above never see the module
				// at all and the import read `unused`. Mark the prefix and every
				// dotted head inside it, the same set the FieldAccess path marks.
				if qt, ok := v.Interface().(*ast.QualifiedType); ok && !IsSynthesizedLine(qt.ModuleLine) {
					if i, ok := byName[qt.Module]; ok {
						used[i] = true
					}
					segs := strings.Split(qt.Module, ".")
					for end := 1; end < len(segs); end++ {
						if i, ok := byName[strings.Join(segs[:end], ".")]; ok {
							used[i] = true
						}
					}
				}
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				field := v.Field(i)
				if field.CanInterface() {
					walk(field)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n))
	}
}

// importItem is one checkable unit of an import statement: an explicit imported
// item, a `self` marker, or an internal/recovered module binding.
type importItem struct {
	sym      *Symbol         // the local binding Symbol defineImport registered
	stmt     *ast.ImportStmt // owning statement (for the fixer)
	name     string          // the bound name (alias when aliased)
	origName string          // pre-alias item name; "" when not aliased
	module   bool            // internal/recovered module binding
	selfItem bool            // the `self` marker in a brace list
	kind     UnusedItemKind  // surgical-removal shape
	nameIdx  int             // index into stmt.Names for brace items; -1 otherwise
	pos      Pos             // diagnostic position (the item's own source position)
}

func (it importItem) message() string {
	switch {
	case it.module:
		return fmt.Sprintf("imported module '%s' is unused — remove the import", it.name)
	case it.selfItem:
		return fmt.Sprintf("imported name 'self' (binding '%s') is unused — remove it from the import", it.name)
	case it.origName != "":
		return fmt.Sprintf("imported name '%s' (bound as '%s') is unused — remove it from the import", it.origName, it.name)
	default:
		// The bound name, not the token: a dotted import binds
		// `Probe.Reading`, and reporting `Probe.Reading` unused reads as a denial
		// that you used it, when what happened is that it never resolved.
		bound := it.name
		if it.sym != nil && it.sym.Name != "" {
			bound = it.sym.Name
		}
		return fmt.Sprintf("imported name '%s' is unused — remove it from the import", bound)
	}
}

// topLevelImportStmts flattens the file's top-level ImportStmt / ImportBlock
// nodes and module-contained imports, skipping synthesized statements
// (the auto-prepended prelude chain carries synth-band positions — see
// cloneImportStmtForInject).
func topLevelImportStmts(nodes []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	add := func(n *ast.ImportStmt) {
		if n == nil || IsSynthesizedLine(n.Line) {
			return
		}
		out = append(out, n)
	}
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case *ast.ImportStmt:
				add(n)
			case *ast.ImportBlock:
				for _, entry := range n.Entries {
					add(entry)
				}
			}
		}
	}
	walk(nodes)
	return out
}

// collectImportItems mirrors defineImport's binding logic to recover each
// item's local Symbol from fa.Definitions (defineImport registers every
// binding there, keyed by the bind node's position). Items that are
// re-exported, or whose Symbol can't be found (an unresolved import already
// carries its own diagnostic), produce no entry.
func collectImportItems(fa *FileAnalysis, stmts []*ast.ImportStmt) []importItem {
	var items []importItem
	for _, n := range stmts {
		if len(n.ModulePath) == 0 {
			continue
		}
		if len(n.Names) == 0 {
			// Empty-name import: valid source syntax no longer produces this
			// shape, but synthesized/recovered ASTs can still carry it. A
			// whole-statement `export` marks it as used by definition.
			if n.ExportAll {
				continue
			}
			bindNode := n.ModulePath[len(n.ModulePath)-1]
			if n.ModuleAlias != nil {
				bindNode = n.ModuleAlias
			}
			pos, ok := importNodePos(bindNode)
			if !ok {
				continue
			}
			sym := fa.Definitions[pos]
			if sym == nil || sym.ModuleScope == nil {
				// Unresolvable module (no loader / degenerate single-file
				// build): usedness can't be decided meaningfully, and piling
				// "unused" onto an unresolved import is noise. Skip.
				continue
			}
			kind := UnusedWholeModule
			if n.ModuleAlias != nil {
				kind = UnusedModuleAlias
			}
			items = append(items, importItem{
				sym:     sym,
				stmt:    n,
				name:    ast.ImportNodeName(bindNode),
				module:  true,
				kind:    kind,
				nameIdx: -1,
				pos:     pos,
			})
			continue
		}

		// Selective import: each brace name is its own item; line-level
		// `export` and per-item `export` flags exempt the re-exported ones.
		for i, nameNode := range n.Names {
			if n.ExportAll || (i < len(n.ExportFlags) && n.ExportFlags[i]) {
				continue
			}
			bindNode := nameNode
			origName := ""
			if i < len(n.Aliases) && n.Aliases[i] != nil {
				bindNode = n.Aliases[i]
				origName = ast.ImportNodeName(nameNode)
			}
			bindPos, ok := importNodePos(bindNode)
			if !ok {
				continue
			}
			sym := fa.Definitions[bindPos]
			if sym == nil || sym.Resolved == nil {
				// Unresolved item: either the name isn't exported by the
				// module (defineImport already errored "has no exported
				// name") or the build context can't resolve modules at all
				// (bare BuildFile, no loader). Don't stack "unused" on top.
				continue
			}
			// Diagnostic position: the item's pre-alias name node (the
			// item as written), even when the binding lives on the alias.
			errPos, ok := importNodePos(nameNode)
			if !ok {
				errPos = bindPos
			}
			itemName := ast.ImportNodeName(bindNode)
			if i >= len(n.Aliases) || n.Aliases[i] == nil {
				if n.Braced && len(n.ModulePath) > 0 {
					if _, isOwner := n.ModulePath[len(n.ModulePath)-1].(*ast.TypeIdent); isOwner {
						itemName = importNameLeaf(itemName)
					}
				}
			}
			items = append(items, importItem{
				sym:      sym,
				stmt:     n,
				name:     itemName,
				origName: origName,
				kind:     UnusedBraceItem,
				nameIdx:  i,
				pos:      errPos,
			})
		}

		// `Owner.{self, ...}`: binds the brace group's explicit owner under
		// the last path segment's name.
		// Line-level `export` re-exports the full selection, self included
		// (prelude's `std/bool.Bool.{self, False, True} export`), so the
		// self item is exempt there like its siblings.
		if n.IncludeParent && !n.ExportAll {
			lastSeg := n.ModulePath[len(n.ModulePath)-1]
			pos, ok := importNodePos(lastSeg)
			if !ok {
				continue
			}
			sym := fa.Definitions[pos]
			if sym == nil || (sym.Resolved == nil && sym.ModuleScope == nil) {
				// Same unresolved-import suppression as above: owner `self`
				// carries Resolved; recovered path-level shapes may carry
				// ModuleScope.
				continue
			}
			errPos := pos
			if n.SelfLine > 0 {
				errPos = Pos{Line: n.SelfLine, Col: n.SelfCol}
			}
			items = append(items, importItem{
				sym:      sym,
				stmt:     n,
				name:     ast.ImportNodeName(lastSeg),
				selfItem: true,
				kind:     UnusedSelfMarker,
				nameIdx:  -1,
				pos:      errPos,
			})
		}
	}
	return items
}

// resolvedImportTarget follows the binding's Resolved chain to the real
// symbol in the source module, or nil for bindings that resolve nowhere
// (internal/recovered module imports, unresolved names).
func resolvedImportTarget(s *Symbol) *Symbol {
	r := s
	for r.Resolved != nil {
		r = r.Resolved
	}
	if r == s {
		return nil
	}
	return r
}

// importNodePos extracts the source position of an import-statement
// *Ident / *TypeIdent the same way defineImport does (LineNum + Col).
func importNodePos(n ast.Node) (Pos, bool) {
	switch v := n.(type) {
	case *ast.Ident:
		return Pos{Line: v.Line, Col: v.Col}, true
	case *ast.TypeIdent:
		return Pos{Line: v.Line, Col: v.Col}, true
	default:
		return Pos{}, false
	}
}

func markImportNodePos(set map[Pos]bool, n ast.Node) {
	if n == nil {
		return
	}
	if pos, ok := importNodePos(n); ok {
		set[pos] = true
	}
}
