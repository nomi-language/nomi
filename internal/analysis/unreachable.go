package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnreachableCodeCode tags the diagnostic for a statement that follows an
// unconditional `return`, `break` or `continue` in the same block.
const UnreachableCodeCode = "unreachable-code"

// checkUnreachable reports the first statement of block that follows a
// `return`, `break` or `continue` statement written directly in it. Nothing
// after such a statement can run, and the IR builder would otherwise have to
// lower dead code. It is an error, as an unused binding is: the analyzer has
// no warning class.
func checkUnreachable(block *ast.Block) (TypeError, bool) {
	if block == nil {
		return TypeError{}, false
	}
	for i, stmt := range block.Stmts {
		var keyword string
		if es, ok := stmt.(*ast.ExprStmt); ok {
			stmt = es.Expr
		}
		switch stmt.(type) {
		case *ast.Return:
			keyword = "return"
		case *ast.Break:
			keyword = "break"
		case *ast.Continue:
			keyword = "continue"
		default:
			continue
		}
		if i+1 >= len(block.Stmts) {
			return TypeError{}, false
		}
		line, col := stmtStart(block.Stmts[i+1])
		if line == 0 || IsSynthesizedLine(line) {
			return TypeError{}, false
		}
		if col == 0 {
			col = 1
		}
		return TypeError{
			Line:    line,
			Col:     col,
			Message: fmt.Sprintf("unreachable code after %s", keyword),
			Code:    UnreachableCodeCode,
		}, true
	}
	return TypeError{}, false
}

// stmtStart is the position of a statement's first token: the leftmost
// operand of a binary or pipe, the callee of a call, the object of a field
// access.
func stmtStart(n ast.Node) (int, int) {
	for {
		switch v := n.(type) {
		case *ast.Binary:
			if v.Left == nil {
				return v.Line, v.Col
			}
			n = v.Left
		case *ast.Call:
			if v.Func == nil {
				return v.Line, v.Col
			}
			n = v.Func
		case *ast.FieldAccess:
			if v.Object == nil {
				return v.Line, v.Col
			}
			n = v.Object
		case *ast.ExprStmt:
			if v.Expr == nil {
				return v.Line, v.Col
			}
			n = v.Expr
		case *ast.Binding:
			return v.Line, v.Col
		default:
			return nodeLineCol(n)
		}
	}
}
