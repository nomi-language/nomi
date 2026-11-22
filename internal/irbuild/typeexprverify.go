package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// recordedTypeExpr is the type the checker recorded for te, a type
// expression of the source being lowered (analysis.FileAnalysis.TypeExprTypes
// of srcFA). ok is false when it holds no te; t is nil when the checker
// recorded that te names no type.
func (g *gen) recordedTypeExpr(te ast.TypeExpr) (t analysis.Type, ok bool) {
	if fa := g.srcFA(); fa != nil {
		t, ok = fa.TypeExprTypes[te]
	}
	return t, ok
}
