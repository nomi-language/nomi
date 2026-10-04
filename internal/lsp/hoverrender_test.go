package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"
)

// renderHover is the hover renderer textDocumentHover uses, without a file.
func renderHover(sym *analysis.Symbol) string { return hoverdoc.Render(sym) }

func renderHoverWithAnalysis(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	return hoverdoc.RenderWithAnalysis(sym, fa)
}

// buildFile analyzes one file with no stdlib and no prelude.
func buildFile(nodes []ast.Node) *analysis.FileAnalysis {
	return analysis.BuildFileWithStdlib(nodes, nil, nil, "", nil)
}
