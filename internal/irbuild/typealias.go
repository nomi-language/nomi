package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `typealias Handler (String, String) -> Result<String, String>` — a MODULE-LEVEL
// transparent synonym.
//
// # An alias lowers to nothing
//
// An alias introduces no type. `Count` is `Int`, so a parameter annotated
// `Count` wants `Int`'s kind and there is no def and no mirror: an annotation
// that spells the alias has the target's type in the checker's record, which
// typeOf projects, and blocklocaltype.go's BLOCK-local alias is the same.
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
// `TargetTypeExpr` nil. As a value type it names the interface conjunction
// the checker resolved, which typeOf projects from the checker's record
// (conjunction.go).
//
// In a `where` clause it names a conjunction of bounds. dict.go already carries a conjunction there —
// `dictTypeParam.bounds` is a LIST because `where T: Display and Greet` is one
// type parameter with two contracts — so an alias over a conjunction needs no
// representation of its own: the checker types the bound as the conjunction,
// and its members are the bounds (boundTypes).
//
// `bounds_test.nomi` writes
// `fn describe<T>(value: T) where T: Showable and Tagged` and
// `fn describe_alias<T>(value: T) where T: ShowAndTag` over the same body, and
// both lower because `dictSeamFor` reads `ShowAndTag` as its conjuncts.
//
// Whatever the seam turns down — a generic interface bound, an unlowerable
// one — is turned down identically whether it was spelled directly or
// through an alias, since both are the same checker types.

// moduleAliasDecl reports a module-level alias whose target the builder cannot
// represent, and is silent for one it can.
//
// The alias itself is nothing to build — it introduces no type — so a
// representable target produces nothing at all. An unrepresentable one is
// refused HERE rather than at each use, because the declaration is the position
// a programmer can act on and the uses are consequences of it.
//
// A BOUND alias is refused only when one of the interfaces it names is one this
// builder cannot dispatch through. The conjunction ITSELF needs no
// representation — `dictTypeParam.bounds` is already a list — so the alias is
// nothing to build for the same reason the transparent form is.
func (g *gen) moduleAliasDecl(t *ast.TypeAlias) {
	if len(t.TypeParams) > 0 {
		// A generic alias names a type only at a use's type arguments
		// (typeOf), where an unrepresentable one is refused.
		return
	}
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
// conjunction this gen cannot dispatch through, and reports whether there
// was one.
//
// THE SAME RESOLUTION THE USE SITE READS, deliberately, so the declaration
// and the use cannot disagree: `dictSeamFor` turns down a bound the checker
// typed as an interface with no def here, or one that is not `lowerable`
// (boundIfaces), and a declaration check with its own opinion would either
// refuse an alias the seam accepts or accept one the seam then refuses
// under `generic function` — which is the two-keys-for-one-gap shape this
// file's header measures.
func (g *gen) unresolvableBoundConjunct(t *ast.TypeAlias) (string, bool) {
	for _, b := range t.Bounds {
		types := g.boundTypes([]ast.TypeExpr{b})
		if types == nil {
			return analysis.TypeExprBaseName(b), true
		}
		for _, it := range types {
			if d := g.ifaceOfType(it); d == nil || !d.lowerable {
				return it.Name, true
			}
		}
	}
	return "", false
}
