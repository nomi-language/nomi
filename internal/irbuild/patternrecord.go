package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A pattern's types are the checker's. It resolved each pattern against
// the value it takes apart and recorded the type of that value
// (analysis.FileAnalysis.PatternTypes) and, for a head that names a
// variant, which variant (PatternVariants). The builder reads both: the
// kind a destructuring parameter takes, whether a head names its value's
// distinct type, and which variant of the subject's enum an arm tests. It
// looks no pattern's head up by name.

// patternKind is the kind of the value pat matches: the projection of the
// type the checker recorded for it. ok is false where the checker
// recorded none.
func (g *gen) patternKind(pat ast.Node) (kind, bool) {
	fa := g.srcFA()
	if fa == nil {
		return kindInvalid, false
	}
	t, ok := fa.PatternTypes[pat]
	if !ok {
		return kindInvalid, false
	}
	return g.project(t), true
}

// patternRefines reports whether pat's head names a subtype (spec §4
// *Subtypes*), `Port(n)`: the checker recorded the subtype as the type pat
// matches.
func (g *gen) patternRefines(pat *ast.EnumPattern) bool {
	fa := g.srcFA()
	if fa == nil {
		return false
	}
	dt, ok := fa.PatternTypes[pat].(*analysis.DistinctType)
	return ok && dt.Subtype
}

// patternVariant is the declared name of the variant head, a pattern's
// head, names. ok is false for a head that names its value's own type, a
// distinct type or a struct.
func (g *gen) patternVariant(head ast.TypeExpr) (string, bool) {
	if head == nil {
		return "", false
	}
	if fa := g.srcFA(); fa != nil {
		v, ok := fa.PatternVariants[head]
		return v, ok
	}
	return "", false
}
