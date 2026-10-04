package semtokens_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// buildFile analyzes one file with no stdlib and no prelude.
func buildFile(nodes []ast.Node) *analysis.FileAnalysis {
	return analysis.BuildFileWithStdlib(nodes, nil, nil, "", nil)
}
