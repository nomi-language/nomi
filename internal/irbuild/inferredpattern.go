package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The type of a DESTRUCTURING lambda parameter the checker solved and nobody
// wrote down.
//
// A lambda's unannotated plain parameter is resolved through the analyzer:
// checkLambdaExpecting solves it from the expected FuncType at the call site
// and records the answer on the parameter's SymbolParam, which
// `inferredParamKind` reads back (see inferred.go). A destructuring parameter
// has no name and so no symbol: `|acc = Map.empty(), (k, v)| …` writes a
// pattern where the name would go. The checker records the type it checked the
// whole pattern against in FileAnalysis.LambdaPatternTypes, keyed by the
// pattern node, and this reads it back. The names the pattern binds cannot
// stand in for it: a wildcard binds nothing, so `|(_, v)|` alone does not say
// what its first component is.
//
// inferred.go's safety net covers this as it covers a plain parameter: a lambda
// is only lowered through a position whose kind the builder derived
// independently, so a wrong kind is caught as a kind mismatch and refused.

// inferredPatternKind is the kind of the value a head-less destructuring lambda
// parameter receives, as the checker recorded it. Reports false when the
// checker recorded nothing or its type has no representation, and the caller
// keeps its refusal.
func (g *gen) inferredPatternKind(pat ast.Node) (kind, bool) {
	if g.fa == nil {
		return kindInvalid, false
	}
	ty, found := g.fa.LambdaPatternTypes[pat]
	if !found {
		return kindInvalid, false
	}
	k := g.project(ty)
	// kindInvalid: lookup — asks whether the checker's type has a representation; a miss leaves the caller's own refusal to name the parameter.
	if k == kindInvalid {
		return kindInvalid, false
	}
	return k, true
}
