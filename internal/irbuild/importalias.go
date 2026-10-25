package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A type a selective import binds under another name:
//
//	import std/currency.{Error as CurrencyError}
//
//	enum Problem {
//	    Bad {error: CurrencyError}
//	}
//
// The std type families (stdenum.go, opaque.go, stdstruct.go, stdhost.go,
// stdgenstruct.go, stdgenhost.go, stdiface.go) anchor a spec under its
// DECLARED name in this file's scope, so `CurrencyError` reached none of them
// and every type that named it, Problem here, was declined. A user type
// imported under an alias already lowers, because foreignType follows the
// import's symbol to the declaration whatever it is spelled.
//
// So typeOf asks this last, after every name-keyed table missed: when the name
// is an import of this file that resolves to a declaration of a different
// name, the answer is the checker's solved type for that declaration,
// projected by (Origin, Name) the way an inferred position is (inferred.go).
// No name is guessed, and a local declaration or a block-local binding of the
// same spelling has already answered above.

// aliasedImport is the declaration a name this file imports under an alias
// resolves to, or nil when the name is not such an import.
func (g *gen) aliasedImport(name string) *analysis.Symbol {
	if g.fa == nil || g.fa.ModuleScope == nil {
		return nil
	}
	local := g.fa.ModuleScope.LookupLocal(name)
	if local == nil || local.Resolved == nil {
		return nil
	}
	sym := resolvedScopeSymbol(g.fa.ModuleScope, name)
	if sym == nil || sym.Name == name || sym.Type == nil {
		return nil
	}
	return sym
}

// aliasedTypeKind is the kind of a type named through an aliased import, with
// its type arguments when it is generic. The bool reports whether the name was
// an aliased import of a type this answers for.
func (g *gen) aliasedTypeKind(name string, params []ast.TypeExpr) (kind, bool) {
	sym := g.aliasedImport(name)
	if sym == nil {
		return kindInvalid, false
	}
	if len(params) == 0 {
		k := g.project(sym.Type)
		// kindInvalid: lookup — a miss leaves the caller's own resolution of the name to refuse it.
		return k, k != kindInvalid
	}
	args := make([]kind, len(params))
	for i, p := range params {
		args[i] = g.typeOf(p)
		// kindInvalid: propagates — a refused type ARGUMENT already reported.
		if args[i] == kindInvalid {
			return kindInvalid, false
		}
	}
	switch t := sym.Type.(type) {
	case *analysis.StructType:
		// `Set<Int>` as `Bag<Int>`: a generic std struct.
		return g.genStructOfType(t.Origin, t.Name, args)
	case *analysis.DistinctType:
		// `Sender<Int>` as `Out<Int>`: a generic std host type.
		return g.genHostOfType(t.Origin, t.Name, args)
	}
	return kindInvalid, false
}
