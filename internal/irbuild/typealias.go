package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `typealias Handler (String, String) -> Result<String, String>` — a MODULE-LEVEL
// transparent synonym.
//
// # It is nothing to emit, and that is the whole shape of the work
//
// An alias introduces no type. `Count` IS `Int`, so a parameter annotated
// `Count` wants `Int`'s kind and there is no def, no Go declaration, no mirror
// and no import. blocklocaltype.go does the same for a BLOCK-local alias
// (`typeOf`'s `blockLocalAlias` arm); this file is one lookup one table over,
// not a representation.
//
// # Cycles
//
// An alias to a function type makes a callable-valued parameter reachable
// through a second spelling, and a directly-spelled function type already
// lowers, so the alias reaches a path that is already served. Cycles cannot
// arise: the front end rejects a cyclic alias outright, before the builder
// sees anything:
//
//	typealias A (A) -> Int      line 1, col 14: unknown type "A"
//	typealias A B               line 1, col 13: unknown type "B"
//	typealias B A               line 2, col 13: unknown type "A"
//
// An alias may only name a type already resolved, so the alias graph is a DAG
// by construction. `aliasResolving` below is therefore a BELT, not the
// mechanism: it costs one slice lookup per annotation and converts a
// hypothetical stack overflow into an ordinary refusal, which is the right
// trade for a crash class even when the population is provably empty. If it
// ever fires, the front-end claim above has changed and the refusal says so at
// that moment.
//
// # A BOUND alias is a different declaration wearing the same node
//
// `typealias Named Display and Debug` populates `Bounds` and leaves
// `TargetTypeExpr` nil. The checker rejects using one as a value type, so it
// cannot reach a signature position — and the two forms share `*ast.TypeAlias`,
// so `moduleAliasKind` must still DECLINE for a nil target rather than hand back
// kindInvalid under an arm claiming to have resolved something.
//
// What it names is a CONJUNCTION OF BOUNDS, and that has exactly one legal
// position: a `where` clause. dict.go already carries a conjunction there —
// `dictTypeParam.bounds` is a LIST because `where T: Display and Greet` is one
// type parameter with two contracts — so an alias over a conjunction needs no
// representation of its own. It needs the names EXPANDED before
// `dictBoundIface` sees them, which is `boundAliasConjuncts` below.
//
// `bounds_test.nomi` writes
// `fn describe<T>(value: T) where T: Showable and Tagged` and
// `fn describe_alias<T>(value: T) where T: ShowAndTag` over the same body, and
// both lower because `dictSeamFor` resolves `ShowAndTag` to its conjuncts.
//
// THE EXPANSION IS NAMES ONLY AND STOPS AT `dictBoundIface`. Whatever that
// resolver turns down — a generic interface bound, an unlowerable one — is
// turned down identically whether it was spelled directly or through an alias.
// So this cannot admit a bound the direct spelling refuses, which is the
// property that keeps it out of the generic-interface work.

// moduleAliasKind is the kind a MODULE-LEVEL `typealias` names, reporting
// whether the name was one.
//
// Asked from typeOf beside the module type table, because an alias and a type
// declaration cannot share a name — the checker refuses the collision — so the
// two lookups are disjoint and their order carries no meaning. It must stay
// ABOVE the stdlib lookups for the reason every other declaration lookup does:
// a program's own `Duration` alias wins over std's opaque type, which is the
// analyzer's "project wins" rule.
func (g *gen) moduleAliasKind(name string) (kind, bool) {
	sym := resolvedTypeSymbol(g.fa, name)
	if sym == nil {
		return kindInvalid, false
	}
	alias, isAlias := sym.Node.(*ast.TypeAlias)
	if !isAlias || alias.TargetTypeExpr == nil {
		// Not an alias, or the BOUND form, which has no target to expand.
		return kindInvalid, false
	}
	if local := g.fa.ModuleScope.Lookup(name); local == nil || local.Resolved != nil {
		// IMPORTED. The target was spelled in the declaring file, so its
		// names resolve in THAT file's scope: `typealias Board Set<Cell>`
		// names a `Cell` this file may never import. The checker resolved
		// the target there and recorded it on the declaration's symbol.
		return g.importedAliasKind(sym), true
	}
	for _, open := range g.aliasResolving {
		if open == name {
			// Unreachable while the front end rejects a cyclic alias; see the
			// file comment. Reported rather than recursed so the failure is a
			// refusal instead of a stack overflow.
			return kindInvalid, true
		}
	}
	g.aliasResolving = append(g.aliasResolving, name)
	k := g.typeOf(alias.TargetTypeExpr)
	g.aliasResolving = g.aliasResolving[:len(g.aliasResolving)-1]
	// kindInvalid: propagates — an alias over an unrepresentable target is
	// still an ALIAS, and the declaration is where that gets named. Answering
	// true keeps the refusal on the alias rather than letting the annotation
	// fall through to `non-local type`, which would be a claim that nothing
	// declares the name.
	return k, true
}

// importedAliasKind is the kind an alias declared in ANOTHER file names: the
// projection of the target type the checker resolved in the declaring file's
// scope. Walking the target's spelling here instead would resolve its names in
// the using file's scope, which binds neither the declaring file's private
// types nor the aliases it did not import.
func (g *gen) importedAliasKind(sym *analysis.Symbol) kind {
	// kindInvalid: propagates — an imported alias over an unrepresentable
	// target is refused where its declaring file's moduleAliasDecl names it.
	return g.project(sym.Type)
}

// qualifiedAliasKind is the kind `shapes.Board` names when `Board` is a
// module-level `typealias` in the file bound to `shapes`, reporting whether it
// was one. The same transparency as moduleAliasKind, reached through the
// qualifier's scope rather than this file's.
func (g *gen) qualifiedAliasKind(t *ast.QualifiedType) (kind, bool) {
	qualifier, name, ok := qualifiedTypeParts(t)
	if !ok {
		return kindInvalid, false
	}
	sym := resolvedScopeSymbol(g.moduleScope(qualifier), name)
	if sym == nil {
		return kindInvalid, false
	}
	if alias, isAlias := sym.Node.(*ast.TypeAlias); !isAlias || alias.TargetTypeExpr == nil {
		return kindInvalid, false
	}
	return g.importedAliasKind(sym), true
}

// moduleAliasDecl reports a module-level alias whose target the builder cannot
// represent, and is silent for one it can.
//
// The alias itself is nothing to build — it introduces no type — so a
// representable target produces nothing at all. An unrepresentable one is
// refused HERE rather than at each use, because the declaration is the position
// a programmer can act on and the uses are consequences of it. That is
// blockAliasDecl's rule for the block-local form, applied one table over.
//
// A BOUND alias is refused only when one of the interfaces it names is one this
// builder cannot dispatch through. The conjunction ITSELF needs no
// representation — `dictTypeParam.bounds` is already a list — so the alias is
// nothing to build for the same reason the transparent form is.
func (g *gen) moduleAliasDecl(t *ast.TypeAlias) {
	if t.TargetTypeExpr == nil {
		// kindInvalid is not involved: a bound alias has no target at all, so
		// its own conjuncts are what can fail to resolve.
		if name, bad := g.unresolvableBoundConjunct(t); bad {
			g.reject("type alias", t.Name+" bounds "+name, t)
		}
		return
	}
	// kindInvalid: reports — refuses the alias by name at its declaration.
	if g.typeOf(t.TargetTypeExpr) == kindInvalid {
		g.reject("type alias", t.Name, t)
	}
}

// unresolvableBoundConjunct names the first interface in a bound alias's
// conjunction that `dictBoundIface` cannot dispatch through, and reports
// whether there was one.
//
// THE SAME RESOLVER THE USE SITE ASKS, deliberately, so the declaration and the
// use cannot disagree: `dictSeamFor` turns down a bound whose name resolves to
// nothing or to an interface that is not `lowerable`, and a declaration check
// with its own opinion would either refuse an alias the seam accepts or accept
// one the seam then refuses under `generic function` — which is the two-keys-
// for-one-gap shape this file's header measures.
func (g *gen) unresolvableBoundConjunct(t *ast.TypeAlias) (string, bool) {
	for _, name := range g.expandBoundAliases(boundBaseNames(t.Bounds)) {
		d, found := g.dictBoundIface(name)
		if !found || !d.lowerable {
			return name, true
		}
	}
	return "", false
}

// boundBaseNames is a bound list's base type names, in source order.
func boundBaseNames(bounds []ast.TypeExpr) []string {
	out := make([]string, 0, len(bounds))
	for _, b := range bounds {
		if n := analysis.TypeExprBaseName(b); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// expandBoundAliases replaces every BOUND ALIAS in a bound-name list with the
// names it spells, recursively, deduped, in source order.
//
// Recursive because the language allows it: ast.TypeAlias' own comment says a
// bound alias is usable "in `where` bounds and in other bound-alias
// definitions", so `typealias A Display and Debug` / `typealias B A and Greet`
// is legal and one pass would leave `A` unresolved.
//
// A NAME THAT IS NOT A BOUND ALIAS PASSES THROUGH UNCHANGED, which is what makes
// this additive: a directly-spelled bound list is its own expansion, so every
// seam that resolved before resolves identically and this can only turn a
// refusal into a lowering. Falsifiable in one direction — any generic function
// that lowered before and stops refutes it.
//
// # THE CYCLE BELT, and why it is a belt
//
// `g.aliasResolving` is reused rather than duplicated, so a bound alias and a
// transparent one cannot recurse through each other unseen. The front end
// already rejects a cyclic alias — typealias.go's header measures three
// spellings, each answering `unknown type` — so the alias graph is a DAG by
// construction and this converts a hypothetical stack overflow into a bound
// silently dropped rather than a crash in a corpus sweep. Dropping rather than
// refusing is deliberate: a dropped bound makes `dictSeamFor` decline, which
// refuses the FUNCTION by name, and that is a position a programmer can act on.
func (g *gen) expandBoundAliases(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	var walk func([]string)
	walk = func(ns []string) {
		for _, n := range ns {
			alias, isBoundAlias := g.boundAliasConjuncts(n)
			if !isBoundAlias {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
				continue
			}
			if slices.Contains(g.aliasResolving, n) {
				continue
			}
			g.aliasResolving = append(g.aliasResolving, n)
			walk(alias)
			g.aliasResolving = g.aliasResolving[:len(g.aliasResolving)-1]
		}
	}
	walk(names)
	return out
}

// boundAliasConjuncts is the base names a BOUND alias spells, reporting whether
// the name was one.
//
// `resolvedTypeSymbol` rather than a table of this file's own declarations, for
// the reason moduleAliasKind uses it: an alias reachable through an import is
// the same declaration and must expand the same way.
func (g *gen) boundAliasConjuncts(name string) ([]string, bool) {
	sym := resolvedTypeSymbol(g.fa, name)
	if sym == nil {
		return nil, false
	}
	alias, isAlias := sym.Node.(*ast.TypeAlias)
	if !isAlias || alias.TargetTypeExpr != nil || len(alias.Bounds) == 0 {
		return nil, false
	}
	return boundBaseNames(alias.Bounds), true
}
