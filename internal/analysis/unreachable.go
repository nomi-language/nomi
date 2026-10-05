package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnreachableCodeCode tags the diagnostic for a statement that follows a
// statement that always exits its block.
const UnreachableCodeCode = "unreachable-code"

// checkUnreachable reports the first statement of block that follows a
// statement that always exits: a `return`, `break` or `continue`; an `if`
// with an `else` whose every branch always exits; a `case` whose every arm
// always exits (a `case` is exhaustive, so one of its arms runs); or a block
// statement holding a statement that always exits. Nothing after such a
// statement can run, and the IR builder would otherwise have to lower dead
// code. It is an error, as an unused binding is: the analyzer has no warning
// class.
//
// Two things that stop a body are not exits here. `todo` is a statement of
// type Unit, so code after it stays legal while a function is being stubbed
// out. A call never counts: no Nomi function is known not to return.
func checkUnreachable(block *ast.Block) (TypeError, bool) {
	if block == nil {
		return TypeError{}, false
	}
	for i, stmt := range block.Stmts {
		ex := alwaysExits(stmt)
		if ex == 0 {
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
			Message: unreachableMessage(stmt, ex),
			Hints:   []string{"remove it"},
			Code:    UnreachableCodeCode,
		}, true
	}
	return TypeError{}, false
}

// exitSet records which of `return`, `break` and `continue` a statement that
// always exits may leave by. Zero means the statement does not always exit.
type exitSet uint8

const (
	exitReturn exitSet = 1 << iota
	exitBreak
	exitContinue
)

// alwaysExits answers how stmt always leaves its block, or 0 when some path
// through it falls through to the next statement.
func alwaysExits(stmt ast.Node) exitSet {
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	switch s := stmt.(type) {
	case *ast.Return:
		return exitReturn
	case *ast.Break:
		return exitBreak
	case *ast.Continue:
		return exitContinue
	case *ast.Block:
		if s == nil {
			return 0
		}
		for _, inner := range s.Stmts {
			if ex := alwaysExits(inner); ex != 0 {
				return ex
			}
		}
	case *ast.If:
		if s == nil || s.Then == nil || s.Else == nil {
			return 0
		}
		then, els := alwaysExits(s.Then), alwaysExits(s.Else)
		if then == 0 || els == 0 {
			return 0
		}
		return then | els
	case *ast.Case:
		if s == nil || len(s.Branches) == 0 {
			return 0
		}
		var all exitSet
		for _, br := range s.Branches {
			ex := alwaysExits(br.Body)
			if ex == 0 {
				return 0
			}
			all |= ex
		}
		return all
	}
	return 0
}

// unreachableMessage names the statement that exits and how it exits.
func unreachableMessage(stmt ast.Node, ex exitSet) string {
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	verb := "exits"
	switch ex {
	case exitReturn:
		verb = "returns"
	case exitBreak:
		verb = "breaks"
	case exitContinue:
		verb = "continues"
	}
	switch stmt.(type) {
	case *ast.Return:
		return "unreachable code after return"
	case *ast.Break:
		return "unreachable code after break"
	case *ast.Continue:
		return "unreachable code after continue"
	case *ast.If:
		return fmt.Sprintf("unreachable code: the `if` above %s in every branch", verb)
	case *ast.Case:
		return fmt.Sprintf("unreachable code: the `case` above %s in every arm", verb)
	}
	return fmt.Sprintf("unreachable code: the block above always %s", verb)
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
