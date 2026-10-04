package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// RedundantPreludeImportCode tags every redundant-prelude-import TypeError /
// LSP diagnostic so the code-action handler can recognize the class without
// sniffing message text.
const RedundantPreludeImportCode = "redundant-prelude-import"

// CheckRedundantPreludeImports reports a TypeError for every imported item
// that re-imports a name the prelude already puts in scope. Writing
// `import std/iter.Iter` in a user file binds `Iter` to exactly the symbol
// the prelude's `std/iter.Iter export` already bound, so the import is inert
// text: deleting it cannot change what the file means.
//
// This is distinct from CheckUnusedImports. A redundant prelude import is
// USED — `Iter.loop(...)` references it — so the usedness scan is satisfied
// and says nothing. What makes it reportable is that the binding it
// establishes is identical to one the file already had.
//
// Granularity, position, and the surgical-removal shape are shared with the
// unused-import diagnostic: both produce []UnusedImport, so the LSP quick-fix
// and organize-imports pruning handle them with one code path.
//
// SCOPE — the rule is derived, not curated. The prelude reaches a file as its
// module scope's PARENT (buildFileWithStdlibAtPath: `NewScope(primitives)`),
// so "the prelude already provides this name" is a parent-chain lookup
// against whatever prelude.nomi actually re-exports. A project build (`nomi
// check`, `nomi run`) also prepends the prelude's imports to the file itself
// and resolves them against its own stdlib analysis; injectedPreludeTargets
// covers that path. Two consequences fall out for free:
//
//   - stdlib modules are exempt. std.Load passes nil primitives, so their
//     module scope has no parent and nothing is ever redundant there. Their
//     explicit imports are load-bearing.
//   - bare BuildFile inputs (no stdlib, no prelude) are exempt for the same
//     reason.
//
// WHAT IS NOT REDUNDANT, and why each case must survive:
//
//   - a whole-file import (`import std/strings`) binds a file API object;
//     the prelude deliberately exports no file API objects, so `strings.foo`
//     needs it.
//   - an aliased item (`import std/iter.Iter as It`) establishes a NEW name.
//   - a drill-through the prelude stops short of
//     (`import std/iter.Iter.{loop}` binds bare `loop`;
//     `import std/comparable.Ordering.{Less}` binds bare `Less`) — the
//     prelude exports the owner, not these members.
//   - a same-named item from a DIFFERENT module than the prelude's. Identity
//     is compared through the resolved declaration, not the spelling, so a
//     user module's own `Result` is never confused with std's.
//
// Re-exports are exempt via collectImportItems, which skips them outright —
// that is what keeps prelude.nomi itself (every line a re-export) quiet.
func CheckRedundantPreludeImports(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	redundant := FindRedundantPreludeImports(fa, nodes)
	if len(redundant) == 0 {
		return nil
	}
	errs := make([]TypeError, 0, len(redundant))
	for _, r := range redundant {
		errs = append(errs, TypeError{
			Line:    r.Pos.Line,
			Col:     r.Pos.Col,
			Message: r.Message,
			Code:    RedundantPreludeImportCode,
		})
	}
	return errs
}

// FindRedundantPreludeImports returns one UnusedImport per imported item whose
// binding duplicates a prelude binding. It is the structured query both the
// diagnostic (via CheckRedundantPreludeImports) and the LSP fix consume. See
// CheckRedundantPreludeImports' doc for the scope rule and exemptions.
func FindRedundantPreludeImports(fa *FileAnalysis, nodes []ast.Node) []UnusedImport {
	if fa == nil || fa.ModuleScope == nil || fa.ModuleScope.Parent == nil {
		return nil // no prelude in scope: nothing can duplicate it
	}
	stmts := topLevelImportStmts(nodes)
	if len(stmts) == 0 {
		return nil
	}
	prelude := fa.ModuleScope.Parent
	injected := injectedPreludeTargets(fa, nodes)

	var out []UnusedImport
	for _, it := range collectImportItems(fa, stmts) {
		if it.module || (it.selfItem && it.sym.Resolved == nil) {
			// A file API object. The prelude exports none, so a module
			// binding can never duplicate one. A `self` item under a type
			// owner (`std/maybe.Maybe.{self}`) binds the type, and is
			// checked like any other item.
			continue
		}
		if it.origName != "" {
			continue // aliased: the alias is a new name
		}
		name := it.name
		if it.sym != nil && it.sym.Name != "" {
			name = it.sym.Name
		}
		target := resolvedImportTarget(it.sym)
		if target == nil {
			continue
		}
		preludeSym := prelude.Lookup(name)
		if !injected[name][target] && !sameImportTarget(it.sym, preludeSym) {
			// Same spelling, different declaration — e.g. a user module
			// exporting its own `Result`. Shadowing is the point; keep it.
			continue
		}
		out = append(out, UnusedImport{
			Stmt:     it.stmt,
			Name:     name,
			ItemKind: it.kind,
			NameIdx:  it.nameIdx,
			Pos:      it.pos,
			Message: fmt.Sprintf(
				"imported name '%s' is already in scope from the prelude — remove it from the import",
				name),
		})
	}
	return out
}

// injectedPreludeTargets answers, by bound name, the declarations the prelude
// imports a project build prepends to a user file resolve to. A project build
// analyzes its own copy of every stdlib file it reaches, so the parent scope's
// prelude symbols (built from the shared stdlib) name different declaration
// objects than the file's explicit imports do; the prepended imports resolve
// within the same build as the file's own, so their targets compare by
// pointer. Empty for a single-file build, which prepends nothing.
func injectedPreludeTargets(fa *FileAnalysis, nodes []ast.Node) map[string]map[*Symbol]bool {
	var stmts []*ast.ImportStmt
	for _, n := range nodes {
		if s, ok := n.(*ast.ImportStmt); ok && isPreludeInjectLine(s.Line) {
			stmts = append(stmts, s)
		}
	}
	if len(stmts) == 0 {
		return nil
	}
	out := map[string]map[*Symbol]bool{}
	for _, it := range collectInjectedImportItems(fa, stmts) {
		target := resolvedImportTarget(it.sym)
		if target == nil {
			continue
		}
		if out[it.name] == nil {
			out[it.name] = map[*Symbol]bool{}
		}
		out[it.name][target] = true
	}
	return out
}

// collectInjectedImportItems is collectImportItems over prepended prelude
// imports: every one is a selective import with no export flags, so each
// brace name and each `self` owner is an item.
func collectInjectedImportItems(fa *FileAnalysis, stmts []*ast.ImportStmt) []importItem {
	var items []importItem
	for _, n := range stmts {
		if len(n.ModulePath) == 0 {
			continue
		}
		bind := func(node ast.Node) {
			pos, ok := importNodePos(node)
			if !ok {
				return
			}
			if sym := fa.Definitions[pos]; sym != nil && sym.Resolved != nil {
				items = append(items, importItem{sym: sym, stmt: n, name: sym.Name})
			}
		}
		for i, nameNode := range n.Names {
			if i < len(n.Aliases) && n.Aliases[i] != nil {
				bind(n.Aliases[i])
				continue
			}
			bind(nameNode)
		}
		if n.IncludeParent {
			bind(n.ModulePath[len(n.ModulePath)-1])
		}
	}
	return items
}

// sameImportTarget reports whether two bindings denote the same declaration.
// Comparison walks each binding's Resolved chain to its origin, then compares
// identity by Symbol pointer first and by declaration site second: a project
// build and the prelude can reach one stdlib declaration through separate
// import symbols, which are distinct pointers describing the same type.
func sameImportTarget(a, b *Symbol) bool {
	if a == nil || b == nil {
		return false
	}
	ta, tb := resolvedImportTarget(a), resolvedImportTarget(b)
	if ta == nil || tb == nil {
		return false
	}
	if ta == tb {
		return true
	}
	if ta.Node != nil && ta.Node == tb.Node {
		return true
	}
	// Cross-FileAnalysis case: separate Symbol values for one declaration.
	// A declaration is pinned by its name plus where it was declared.
	if ta.Name != tb.Name || ta.Kind != tb.Kind {
		return false
	}
	if ta.DefinitionFile == "" || ta.DefinitionFile != tb.DefinitionFile {
		return false
	}
	return ta.Pos == tb.Pos
}
