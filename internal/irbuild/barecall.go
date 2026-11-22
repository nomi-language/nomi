package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A call SPELLED like a call to a local function, whose callee is not one.
//
// # Three shapes reach a bare call that is not a local function
//
//	import api.{make}                    -> make("Ada")     a SIBLING FILE's fn
//	import std::strings.String.{length}   -> length("nomi")  a STDLIB METHOD
//	interface F { fn shout(v: self) { label(v) } }           an interface's own
//	                                                          sibling requirement
//
// Each is the call its resolution means, as a bare `loop` is for
// `import std::iter.Iter.loop`, whose record names std's Iter.loop
// (stdIterLoop). The first is irqualcall.go's
// siblingCall; the second is ownerdecl.go's bareStdMethodCall. The third,
// like a bare call to any function of the enclosing impl block, in a body
// or in a parameter default, is the qualified call `F.label(v)` with the
// owner the checker recorded (barecallimpl.go's bareImplCall, and
// stdIfaceOwnCall for a default lowered for std's or another file's
// implementor).
//
// # Resolution is the analyzer's, and the OWNER is checked against std
//
// A selectively imported method binds a PROXY symbol whose Resolved carries the
// declaration and its `OwningType`, so `import std::strings.String.{length as
// len}` answers `String` / `length` for the local spelling `len`. Both halves
// are needed: the local name is not the declared name, and the declared name
// alone does not say which type owns it.
//
// The owner is then confirmed POSITIVELY against the stdlib index rather than
// trusted as a string, and the route is declined outright when this module
// declares a type or an interface of that name. A bare qualifier that
// resolves locally to a DIFFERENT declaration would match a type identity by
// its name alone, a silent wrong answer rather than a refusal.

// resolveSymbol follows a proxy chain to the symbol that really declares a
// name. Bounded, because a proxy chain is data rather than a bounded shape.
func resolveSymbol(sym *analysis.Symbol) *analysis.Symbol {
	for range 16 {
		if sym == nil || sym.Resolved == nil {
			break
		}
		sym = sym.Resolved
	}
	return sym
}

// sameDecl reports whether a and b are one declaration: the same node, or
// one std function in two parses of its module's file, which share its std
// identity (aStd and bStd, analysis.StdFunc; the zero StdFunc for a
// declaration no std file makes) wherever the function sits in each. The
// identity names one declaration of its module, an impl's function by its
// impl's interface and type arguments too, so two functions of one name
// on one receiver are never one here.
//
// A reference to a generic function holds the checker's instantiated copy
// of its symbol, so symbols are compared by their declarations here, not by
// pointer. A std module checked from its text, as an edit or a reference
// editor writes it, is a second parse of the module beside the shared
// analysis's. A shared module's cases are lowered against the shared
// lowering's tables (GenerateStdlibTestIR), so a call's record names the
// second parse's declaration and the route the shared parse's. A listed
// module lowered beside an edited one (std/sql/testing beside std/sql) was
// checked against the shared analysis, so its record names the shared
// parse's declaration, and the route the edited one's twin
// (indexEditedStdTwins). Each pair is one function.
func sameDecl(a, b ast.Node, aStd, bStd analysis.StdFunc) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || !aStd.IsZero() && aStd == bStd
}

// boundSameDecl reports whether the symbol a scope binds, followed through
// imports, declares what sym does (sameDecl).
func boundSameDecl(bound, sym *analysis.Symbol) bool {
	bound = resolveSymbol(bound)
	return bound != nil && sym != nil && (bound == sym || sameDecl(bound.Node, sym.Node, analysis.StdFunc{}, analysis.StdFunc{}))
}

// resolvedBareSymbol follows a bare name in a file's module scope to the symbol
// that really declares it, or nil when the name is not in that scope at all.
func resolvedBareSymbol(fa *analysis.FileAnalysis, name string) *analysis.Symbol {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	return resolveSymbol(fa.ModuleScope.Lookup(name))
}

// resolvedBareSymbolAt is resolvedBareSymbol for the name written at id. A
// name may be imported at the top of an enclosing block (`import
// std::io.print`, which testImport admits), whether or not the module scope
// binds it too; the checker's reference at id is then that import's
// binding, which wins over the module scope's.
func resolvedBareSymbolAt(fa *analysis.FileAnalysis, id *ast.Ident) *analysis.Symbol {
	if fa != nil && len(fa.BlockImportScopes) > 0 {
		if ref := fa.References[analysis.Pos{Line: id.Line, Col: id.Col}]; ref != nil {
			if imp, imported := ref.Node.(*ast.ImportStmt); imported && fa.BlockImportScopes[imp] != nil {
				return resolveSymbol(ref)
			}
		}
	}
	if sym := resolvedBareSymbol(fa, id.Name); sym != nil || fa == nil || fa.ModuleScope == nil {
		return sym
	}
	ref := fa.References[analysis.Pos{Line: id.Line, Col: id.Col}]
	if ref == nil {
		return nil
	}
	if _, imported := ref.Node.(*ast.ImportStmt); !imported {
		return nil
	}
	return resolveSymbol(ref)
}
