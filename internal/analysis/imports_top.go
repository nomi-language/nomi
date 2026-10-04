package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"strings"
)

// checkImportsAtTop enforces ordering of imports and exports relative to
// other top-level statements:
//
//   - At file scope, the canonical order is optional file module header →
//     imports → exports → declarations. Imports must precede exports; both
//     must precede ordinary declarations.
//   - Inside a function body, imports must precede any other statement.
//     (Exports are file-scope only; they are not considered here.)
//   - Nested function bodies follow the body rule recursively.
//
// Mid-block imports silently rebind names; mid-file exports scatter the
// public surface across the file. The rule keeps "what does X mean
// here?" and "what does this module expose?" answerable by reading the
// top of the file.
//
// Other nested blocks (if/else, case arms, expression blocks, lambda
// bodies) are NOT covered by this check — those rarely contain imports
// in real code.
func (c *checker) checkImportsAtTop(nodes []ast.Node) {
	// File scope — three-state walk that handles imports, exports, and
	// recurses into any *ast.FuncDef bodies it finds.
	c.checkOrderingAtFileScope(nodes)
}

// checkOrderingAtFileScope walks top-level nodes with a three-state
// machine:
//
//	state 0 — start: imports OK, exports OK
//	state 1 — saw an export: exports OK, imports rejected
//	state 2 — saw a declaration: imports and exports both rejected
//
// Every misplaced import/export is flagged independently so the user
// sees them all in one pass. *ast.FuncDef nodes are treated as
// declarations and their bodies are checked recursively under the
// body-scope rule.
func (c *checker) checkOrderingAtFileScope(nodes []ast.Node) {
	const (
		stateStart       = 0
		stateAfterExport = 1
		stateAfterDecl   = 2
	)
	state := stateStart
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.ImportStmt:
			if state >= stateAfterExport {
				c.errors = append(c.errors, TypeError{
					Line:    n.Line,
					Col:     n.Col,
					Message: fmt.Sprintf("imports must appear at the top of the file — '%s' came after another statement", importDisplayName(n)),
				})
			}
			continue
		case *ast.ImportBlock:
			if state >= stateAfterExport {
				name := "import block"
				line, col := n.Line, n.Col
				if len(n.Entries) > 0 {
					name = importDisplayName(n.Entries[0])
					line, col = n.Entries[0].Line, n.Entries[0].Col
				}
				c.errors = append(c.errors, TypeError{
					Line:    line,
					Col:     col,
					Message: fmt.Sprintf("imports must appear at the top of the file — '%s' came after another statement", name),
				})
			}
			continue
		}
		// Anything else is a declaration.
		state = stateAfterDecl
		if fn, ok := node.(*ast.FuncDef); ok && fn.Body != nil {
			c.checkImportsAtTopOfStmts(fn.Body.Stmts)
		}
	}
}

// checkImportsAtTopOfStmts flags any import (per-statement or block form)
// that appears after a non-import statement. Every late import is flagged
// independently so the user sees them all in one pass.
//
// Recurses into nested *ast.FuncDef bodies so the same rule applies at
// every depth.
func (c *checker) checkImportsAtTopOfStmts(stmts []ast.Node) {
	sawNonImport := false
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.ImportStmt:
			if sawNonImport {
				c.errors = append(c.errors, TypeError{
					Line:    n.Line,
					Col:     n.Col,
					Message: fmt.Sprintf("imports must appear at the top of this block — '%s' came after another statement", importDisplayName(n)),
				})
			}
			continue
		case *ast.ImportBlock:
			if sawNonImport {
				name := "import block"
				line, col := n.Line, n.Col
				if len(n.Entries) > 0 {
					name = importDisplayName(n.Entries[0])
					line, col = n.Entries[0].Line, n.Entries[0].Col
				}
				c.errors = append(c.errors, TypeError{
					Line:    line,
					Col:     col,
					Message: fmt.Sprintf("imports must appear at the top of this block — '%s' came after another statement", name),
				})
			}
			continue
		case *ast.FuncDef:
			if n.Body != nil {
				c.checkImportsAtTopOfStmts(n.Body.Stmts)
			}
		}
		sawNonImport = true
	}
}

// importDisplayName formats the slash-separated module path of an import
// for use in error messages: `import std/lists` → "std/lists",
// `import a/b.{x}` → "a/b".
func importDisplayName(imp *ast.ImportStmt) string {
	parts := make([]string, 0, len(imp.ModulePath))
	for _, seg := range imp.ModulePath {
		parts = append(parts, ast.ImportNodeName(seg))
	}
	return strings.Join(parts, "/")
}
