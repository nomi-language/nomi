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
//	import std/strings.String.{length}   -> length("nomi")  a STDLIB METHOD
//	interface F { fn shout(v: self) { label(v) } }           an interface's own
//	                                                          sibling requirement
//
// The first two are resolution paths, as ctrlflow.go's iterOwnerCall is for
// `import std/iter.Iter.loop`. The first is irqualcall.go's siblingCall; the
// second is here. The third is refused by its own name, so the refusal names
// what the call is rather than blaming an unlowered callee.
//
// # Resolution is the analyzer's, and the OWNER is checked against std
//
// A selectively imported method binds a PROXY symbol whose Resolved carries the
// declaration and its `OwningType`, so `import std/strings.String.{length as
// len}` answers `String` / `length` for the local spelling `len`. Both halves
// are needed: the local name is not the declared name, and the declared name
// alone does not say which type owns it.
//
// The owner is then confirmed POSITIVELY against the stdlib index rather than
// trusted as a string, and the route is declined outright when this module
// declares a type or an interface of that name. Handing `implCall` a bare
// qualifier that resolves locally to a DIFFERENT declaration would match a type
// identity by its name alone, a silent wrong answer rather than a refusal.

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

// rivalRequirementOwner names another interface implemented for the same
// receiver that declares the same requirement name, or "".
//
// The RECEIVER is what makes two interfaces rivals, not the module: two
// interfaces may share a requirement name freely as long as no single type
// implements both, and a bare name is ambiguous only where one dispatch slot
// has two candidates.
//
// Deterministic in the name it reports, because `g.implOrder` is source order
// and a refusal's detail that varied per run would make the sweep's own output
// nondeterministic.
func (g *gen) rivalRequirementOwner(d *implDef, name string) string {
	for _, other := range g.implOrder {
		if other == d || other.recv != d.recv || other.iface == nil || other.iface == d.iface {
			continue
		}
		if other.iface.methods[name] != nil {
			return other.iface.nomi
		}
	}
	return ""
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
// name the module scope does not bind may still be imported at the top of an
// enclosing block (`import std/io.print`, which testImport admits); the
// checker's reference at id is then that import's binding.
func resolvedBareSymbolAt(fa *analysis.FileAnalysis, id *ast.Ident) *analysis.Symbol {
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
