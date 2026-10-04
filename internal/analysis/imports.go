package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ExtractImportsFromNodes extracts import file paths from parsed AST nodes.
// Handles both standalone `import path` (ImportStmt) and block-form
// `import { path1, path2 }` (ImportBlock). Without the ImportBlock case the
// LSP's reverse-dependency map would silently miss every import inside a
// block-form statement, leaving dependent files unable to receive fresh
// diagnostics when their parent module changes (the bug that surfaced as
// "rename in configuration.nomi doesn't propagate to configuration/dev.nomi
// until LSP restart" — every file in tests/effects/ uses block-form
// imports).
func ExtractImportsFromNodes(nodes []ast.Node, dir string) []string {
	seen := make(map[string]bool)
	var imports []string
	addStmt := func(imp *ast.ImportStmt) {
		modulePath := make([]string, 0, len(imp.ModulePath))
		for _, seg := range imp.ModulePath {
			modulePath = append(modulePath, ast.ImportNodeName(seg))
		}
		// Skip stdlib imports — they're embedded, not on disk
		if len(modulePath) >= 2 && modulePath[0] == "std" {
			return
		}
		filePath := ResolveModulePath(dir, modulePath)
		if !seen[filePath] {
			seen[filePath] = true
			imports = append(imports, filePath)
		}
	}
	var walk func([]ast.Node)
	walk = func(nodes []ast.Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case *ast.ImportStmt:
				addStmt(n)
			case *ast.ImportBlock:
				for _, entry := range n.Entries {
					addStmt(entry)
				}
			}
		}
	}
	walk(nodes)
	return imports
}
