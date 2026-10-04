package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// BuildFile builds analysis for a single file with no stdlib context.
// Exported so analysis_test can use it too.
func BuildFile(nodes []ast.Node) *FileAnalysis {
	return BuildFileWithStdlib(nodes, nil, nil, "", nil)
}

// DiscoverProject is DiscoverProjectWithManifest with the manifest read
// from disk.
func DiscoverProject(entryNodes []ast.Node, projectRoot string, loader FileLoader) (*Project, error) {
	return DiscoverProjectWithManifest(entryNodes, projectRoot, loader, nil)
}

// ExtractImports parses source code and returns the file paths of all
// imported modules.
func ExtractImports(source string, dir string) []string {
	tokens := lexer.Lex(source)
	nodes, _ := parser.ParseWithRecovery(tokens)
	return ExtractImportsFromNodes(nodes, dir)
}

// declaringModuleOfForTest is declaringModuleOf in the shape the
// declaring-module tests assert against: the reported module, or "".
func declaringModuleOfForTest(idx map[string][]string, name string) string {
	mod, _ := declaringModuleOf(idx, name)
	return mod
}

// singleDeclModules lifts a one-module-per-name map into the
// multi-valued shape, for tests that state the simple case directly.
func singleDeclModules(in map[string]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for name, mod := range in {
		out[name] = []string{mod}
	}
	return out
}
