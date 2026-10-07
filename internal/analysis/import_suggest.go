package analysis

import (
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// MissingImport identifies one unresolved reference the file could fix by
// importing an exported stdlib owner. `Member` is the owner name, and the
// rendered import is selective: `std/io` / `std/sets.Set`.
//
// Pos is the 1-based position of the unresolved identifier — exactly where the
// "undefined …" diagnostic lands, so the code-action handler matches the two by
// position. When a type name is exported by more than one module, one
// MissingImport per candidate module is emitted at the same Pos; the handler
// offers each as a quick-fix and (being ambiguous) leaves them out of fixAll.
type MissingImport struct {
	Qualifier  string // the unresolved name, e.g. "io" or "Set"
	ModulePath string // owning module path, e.g. "std/io" or "std/sets"
	Member     string // exported owner name for the selective import
	Pos        Pos    // 1-based position of the unresolved identifier
}

// ImportSpec renders the selective import this suggestion adds:
// `std/io` / `std/sets.Set`.
func (m MissingImport) ImportSpec() string {
	if m.Member == "" {
		return displayImportModulePath(m.ModulePath)
	}
	return displayImportModulePath(m.ModulePath) + "." + m.Member
}

func displayImportModulePath(path string) string {
	return path
}

// FindMissingImports returns the imports the file could add for references it
// never imports. The detector is resolution-driven, not text- or
// scope-reconstructing: a reference needs an import iff the analyzer recorded NO
// reference at its position (it resolved to nothing) and its name is a known
// stdlib export. A recorded reference means it already binds (import, local,
// param), so there's no alias/collision handling to do — a collision would make
// the name resolve. Definition positions are excluded (a `struct Set` declares
// Set; it must not suggest importing it). Project (non-stdlib) modules are out
// of scope for now.
//
//   - Owner references: any unresolved TypeIdent whose name a stdlib module
//     exports (struct/enum/type/alias/interface/module) → selective
//     `std/<mod>.Name`.
func FindMissingImports(fa *FileAnalysis, nodes []ast.Node) []MissingImport {
	if fa == nil || len(fa.StdlibModuleScopes) == 0 {
		return nil
	}
	typeIndex := buildStdlibTypeIndex(fa)
	seen := make(map[Pos]bool)
	var out []MissingImport
	for _, node := range nodes {
		WalkNodes(node, func(n ast.Node) {
			switch v := n.(type) {
			case *ast.TypeIdent: // expression-level type ref (`Set.insert`)
				addTypeSuggestions(fa, typeIndex, v.Name, Pos{Line: v.Line, Col: v.Col}, seen, &out)
			case *ast.SimpleType: // annotation `s: Set`
				addTypeSuggestions(fa, typeIndex, v.Name, Pos{Line: v.Line, Col: v.Col}, seen, &out)
			case *ast.GenericType: // annotation `s: Set<Int>` (the base name)
				addTypeSuggestions(fa, typeIndex, v.Name, Pos{Line: v.Line, Col: v.Col}, seen, &out)
			case *ast.FieldAccess: // expression-level file API ref (`io.inspect`)
				if obj, ok := v.Object.(*ast.Ident); ok {
					addNamespaceSuggestion(fa, obj.Name, Pos{Line: obj.Line, Col: obj.Col}, seen, &out)
				}
			}
		})
	}
	return out
}

func addNamespaceSuggestion(fa *FileAnalysis, name string, pos Pos, seen map[Pos]bool, out *[]MissingImport) {
	if seen[pos] || IsSynthesizedLine(pos.Line) {
		return
	}
	if fa.References[pos] != nil || fa.Definitions[pos] != nil {
		return
	}
	if fa.StdlibModuleScopes[name] == nil {
		return
	}
	seen[pos] = true
	*out = append(*out, MissingImport{Qualifier: name, ModulePath: "std/" + name, Pos: pos})
}

func addTypeSuggestions(fa *FileAnalysis, typeIndex map[string][]string, name string, pos Pos, seen map[Pos]bool, out *[]MissingImport) {
	if seen[pos] || IsSynthesizedLine(pos.Line) {
		return
	}
	if fa.References[pos] != nil || fa.Definitions[pos] != nil { // resolved, or a definition
		return
	}
	mods := typeIndex[name]
	if len(mods) == 0 {
		return
	}
	seen[pos] = true
	for _, modPath := range mods { // one suggestion per candidate module
		*out = append(*out, MissingImport{Qualifier: name, ModulePath: modPath, Member: name, Pos: pos})
	}
}

// buildStdlibTypeIndex maps each stdlib-exported type/module owner name to
// the module(s) that OWN it. The `Public && Resolved == nil` filter is load-bearing: a module's
// scope also holds every prelude/imported name visible inside it (those carry
// Resolved != nil), so without it the index would claim e.g. `Map` is exported
// by every module that imports it. Restricting to owner-like kinds keeps impl
// methods and variant constructors out.
func buildStdlibTypeIndex(fa *FileAnalysis) map[string][]string {
	idx := map[string][]string{}
	seen := map[*Scope]bool{}
	for short, scope := range fa.StdlibModuleScopes {
		indexStdlibScopeTypes(idx, scope, "std/"+short, seen)
	}
	return idx
}

func indexStdlibScopeTypes(idx map[string][]string, scope *Scope, modulePath string, seen map[*Scope]bool) {
	if scope == nil {
		return
	}
	if seen[scope] {
		return
	}
	seen[scope] = true
	for name, sym := range scope.Symbols {
		if sym == nil || sym.Resolved != nil || !sym.Public {
			continue
		}
		if isTypeKind(sym.Kind) {
			idx[name] = append(idx[name], modulePath)
		}
	}
}

func isTypeKind(k SymbolKind) bool {
	switch k {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		return true
	}
	return false
}

// WalkNodes calls fn on n and every node beneath it (ast.Inspect), once
// each: the derive and Debug passes share type annotations among nodes, and a
// node met a second time is skipped with its subtree.
func WalkNodes(n ast.Node, fn func(ast.Node)) {
	if n == nil {
		return
	}
	walkNodesCalls.Add(1)
	seen := map[ast.Node]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		fn(n)
		return true
	})
}

// walkNodesCalls counts WalkNodes calls.
var walkNodesCalls atomic.Int64

// WalkNodesCalls is the number of WalkNodes calls so far, for tests that
// bound how often a request walks a tree.
func WalkNodesCalls() int64 { return walkNodesCalls.Load() }
