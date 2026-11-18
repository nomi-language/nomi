package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// recordedTypeExpr is the type the checker recorded for te, a type
// expression of the source being lowered (analysis.FileAnalysis.TypeExprTypes
// of srcFA), or the type of one in code the builder wrote for a block-local
// declaration (synthTypeExprs). ok is false when neither holds te; t is nil
// when the checker recorded that te names no type.
func (g *gen) recordedTypeExpr(te ast.TypeExpr) (t analysis.Type, ok bool) {
	if fa := g.srcFA(); fa != nil {
		if t, ok = fa.TypeExprTypes[te]; ok {
			return t, true
		}
	}
	t, ok = g.synthTypeExprs[te]
	return t, ok
}
