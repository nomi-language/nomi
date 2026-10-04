package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Redeclaration and shadowing rules:
//
//   - Same-scope duplicates (two declarations of the same name in the
//     same scope, regardless of kind) are an error. This covers
//     top-level decls (fn / type / interface / typealias / once /
//     extern / impl-method), and nested once bindings.
//
//   - Ordinary `name = value` bindings are free to shadow module-level
//     decls — that is the standard immutable-rebind pattern.
//
//   - Ordinary `name = value` bindings MAY shadow a prior same-scope
//     binding or parameter (Gleam/Rust-style shadowing). The newest
//     binding wins for subsequent references and may change the type.
//     Use checkRedeclareInScopeBinding at binding sites.
//
//   - Destructure-introduced names (tuple/struct/map/distinct) have no
//     redeclare check at their definition sites, so they have always
//     permitted same-scope re-binding; this rule brings plain bindings
//     and parameters in line with that.
//
// kindLabel produces a human-readable name for use in error messages.
func kindLabel(s *Symbol) string {
	if s == nil {
		return "name"
	}
	switch s.Kind {
	case SymbolFunction:
		return "function"
	case SymbolType:
		return "type"
	case SymbolTypeAlias:
		return "type alias"
	case SymbolInterface:
		return "interface"
	case SymbolStruct:
		return "type"
	case SymbolEnum:
		return "type"
	case SymbolEnumVariant:
		return "enum variant"
	case SymbolField:
		return "field"
	case SymbolParam:
		return "parameter"
	case SymbolOnce:
		return "once binding"
	case SymbolBinding:
		return "binding"
	case SymbolModule:
		return "module"
	case SymbolInterfaceMethod:
		return "interface function"
	default:
		return "name"
	}
}

// firstDefinitionRelated points at prior, the declaration a redeclaration
// collides with, when the file it is in is known.
func (b *builder) firstDefinitionRelated(prior *Symbol, name string) (RelatedInfo, bool) {
	if prior.Pos.Line <= 0 || prior.Pos.Col <= 0 || IsSynthesizedLine(prior.Pos.Line) {
		return RelatedInfo{}, false
	}
	file := ""
	if prior.SourceFile != "" && prior.SourceFile != b.file.FilePath {
		file = prior.SourceFile
	} else if prior.SourceFile == "" && b.file.Definitions[prior.Pos] != prior {
		return RelatedInfo{}, false
	}
	return nameRelated(file, prior.Pos, name, "'"+name+"' is first defined here"), true
}

// checkRedeclareInScope reports a redeclaration error if `name` is
// already defined in `scope`'s local table at a different position from
// (line, col). Returns true when an error was emitted; callers should
// skip their `scope.Define(...)` call so the original binding remains
// authoritative for subsequent lookups.
func (b *builder) checkRedeclareInScope(scope *Scope, name string, line, col int) bool {
	return b.checkRedeclareInScopeKind(scope, name, line, col, false)
}

// checkRedeclareInScopeBinding is the variant used at ordinary `name = value`
// binding sites. Per the design, a binding may shadow a prior same-scope
// binding or parameter (Gleam/Rust-style); all other same-scope collisions
// remain errors.
func (b *builder) checkRedeclareInScopeBinding(scope *Scope, name string, line, col int) bool {
	return b.checkRedeclareInScopeKind(scope, name, line, col, true)
}

func (b *builder) checkRedeclareInScopeKind(scope *Scope, name string, line, col int, isBinding bool) bool {
	prior := scope.LookupLocal(name)
	if prior == nil {
		return false
	}
	if prior.Pos.Line == line && prior.Pos.Col == col {
		// Same node revisited (idempotent re-entry); not a real duplicate.
		return false
	}
	// Imported modules can share a name with a local declaration: Nomi's
	// field-access semantics fall back to module resolution when a local
	// binding has no field of that name. The canonical case is `pub fn map`
	// alongside `import std.map` in stdlib/lists.nomi. Don't treat the
	// pair as a redeclaration.
	if prior.Kind == SymbolModule {
		return false
	}
	// Public interface methods are the weakest file-scope occupants: they
	// make `Debug.inspect(x)` possible, but any real file-level declaration named
	// `inspect` should own the file export and bare name.
	if prior.Kind == SymbolInterfaceMethod {
		return false
	}
	// A variant's bare name is the weakest declaring occupant of a file
	// scope: any other declaration or import of the same name takes the
	// slot. See variantMayTakeScopeSlot for the rule and its reason.
	if isLocalVariant(prior) {
		return false
	}
	// Rust/Gleam-style shadowing: an ordinary `name = value` binding may
	// shadow a prior same-scope binding or parameter. The new binding wins
	// for subsequent references and may change the type; the caller proceeds
	// to scope.Define, overwriting the entry.
	if isBinding && (prior.Kind == SymbolBinding || prior.Kind == SymbolParam) {
		return false
	}
	e := TypeError{
		Line:    line,
		Col:     col,
		Message: fmt.Sprintf("'%s' is already defined in this scope as a %s", name, kindLabel(prior)),
	}.WithHint("pick a different name")
	if r, ok := b.firstDefinitionRelated(prior, name); ok {
		e = e.WithRelated(r)
	}
	b.file.TypeErrors = append(b.file.TypeErrors, e)
	return true
}

// collidesWithImportedModule reports an error if a local definition's
// name shadows an imported module in the same scope. Returns true if a
// collision was reported (the caller may use this to skip further
// processing, though the symbol is still defined to keep downstream
// checks meaningful).
func (b *builder) collidesWithImportedModule(scope *Scope, name string, line, col int) bool {
	existing := scope.LookupLocal(name)
	if existing == nil || existing.Kind != SymbolModule {
		return false
	}
	b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
		Line: line,
		Col:  col,
		Message: fmt.Sprintf(
			"name '%s' collides with imported module — alias the import (e.g. `import ... as %s2`) or rename this definition",
			name, name),
	})
	return true
}

// importMayTakeScopeSlot answers whether a selectively-imported name may
// occupy `name`'s module-scope slot, reporting a redeclaration when it may
// not. Returns true when the caller should proceed with scope.Define.
//
// The question exists because defineImport is annotation-side while
// declaration stubs run in an earlier pass, so an import's Define always
// writes last and used to overwrite whatever the file declared under that
// name — silently, for every declaring kind. FileAnalysis.ModuleScope was
// therefore not able to answer "which declaration does this name mean in
// this file?", which is the question its consumers ask of it.
//
// What is NOT a collision:
//
//   - A synthesized import. The auto-prepended prelude chain lands in the
//     synth band and has no user-visible position to report at. A local
//     declaration of a prelude name is diagnosed at its own site by
//     checkReservedTypeName.
//   - An import landing on another import's slot. `import std/maybe.{Maybe}`
//     beside the prelude's own injected `Maybe` is the explicit-is-better
//     idiom that stdlib files must write, and two selective imports of the
//     same name have always resolved last-wins. Import-vs-import precedence
//     is a separate open question; this check leaves it exactly as it was.
//   - An import landing on a variant this file declares. The import takes
//     the slot (see variantMayTakeScopeSlot).
//
// Everything else — an import arriving on a name this file declares as an
// interface, struct, enum, distinct type, alias, or function — is reported
// and the declaration keeps the slot.
func (b *builder) importMayTakeScopeSlot(scope *Scope, name string, pos Pos) bool {
	if IsSynthesizedLine(pos.Line) {
		return true
	}
	prior := scope.LookupLocal(name)
	if prior == nil || symbolCameFromImport(prior) {
		return true
	}
	return !b.checkRedeclareInScope(scope, name, pos.Line, pos.Col)
}

// symbolCameFromImport reports whether a scope entry was bound by an import
// statement rather than declared in the file. Import-bound symbols carry the
// ImportStmt as their Node; a declaration carries its own declaring node.
func symbolCameFromImport(sym *Symbol) bool {
	_, imported := sym.Node.(*ast.ImportStmt)
	return imported
}

// variantMayTakeScopeSlot answers whether an enum variant's bare name may
// occupy `name`'s slot in the scope the enum is declared in.
//
// The rule: a variant's bare name yields to every other file-scope name. A
// type, interface, function or import of the same name owns the slot, in
// either source order, and the collision is not an error. Two exceptions
// keep their earlier behavior: between two variants of one name (two enums
// in one file may share a variant name; neither is reachable bare) the later
// one is defined, and a variant still replaces a public interface function's
// entry, the weakest occupant of all (see checkRedeclareInScopeKind).
//
// Why the variant is the one that yields: the spec does not let a same-file
// variant be referenced bare (§8: `Shape.Circle(5.0)`, `.Circle(5.0)` or a
// pattern `.Circle` / `Shape.Circle`; bare `Circle` is the error "variant
// 'Circle' must be qualified through its enum"). Qualified and dot-leading
// references resolve through the enum's Members or the expected type, never
// through this slot. The slot's only job for a local variant is to find the
// variant so that error can name the enum. A type or interface name, on the
// other hand, is needed bare in type position, impl headers and owner calls
// (`String.trim`), and an import exists only to put a name in this slot. So
// letting the variant win made a legal program (std/json's `enum Json { String
// String ... }` beside `import strings.String`) mean the wrong type, and
// letting the other name win makes no legal program ambiguous: the only
// reference that changes meaning is a bare variant reference, which was an
// error either way.
func variantMayTakeScopeSlot(scope *Scope, name string) bool {
	prior := scope.LookupLocal(name)
	return prior == nil || isLocalVariant(prior) || prior.Kind == SymbolInterfaceMethod
}

// isLocalVariant reports whether a scope entry is an enum variant this file
// declares, as opposed to one an import bound (`import shape.Shape.{Circle}`,
// or the prelude's `Some`), which is an import symbol carrying the
// ImportStmt as its Node.
func isLocalVariant(sym *Symbol) bool {
	return sym != nil && sym.Kind == SymbolEnumVariant && !symbolCameFromImport(sym)
}
