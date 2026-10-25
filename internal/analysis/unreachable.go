package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnreachableCodeCode tags the diagnostic for a statement that follows a
// statement that always exits its block.
const UnreachableCodeCode = "unreachable-code"

// checkUnreachable reports the first statement of block that follows a
// statement that always exits: a `return`, `break` or `continue`; an
// `Iter.loop` whose callback holds no `break` (LoopNeverBreaks), which never
// finishes; an `if` with an `else` whose every branch always exits; a `case`
// whose every arm always exits (a `case` is exhaustive, so one of its arms
// runs); or a block statement holding a statement that always exits. Nothing after such a
// statement can run, and the IR builder would otherwise have to lower dead
// code. It is an error, as an unused binding is: the analyzer has no warning
// class.
//
// Two things that stop a body are not exits here. `todo` is a statement of
// type Unit, so code after it stays legal while a function is being stubbed
// out. A call never counts: no Nomi function is known not to return.
//
// It runs once the block's statements are resolved, since whether a bare
// `loop(...)` is std's `Iter.loop` is the reference recorded at its name.
func checkUnreachable(fa *FileAnalysis, block *ast.Block) (TypeError, bool) {
	if block == nil {
		return TypeError{}, false
	}
	for i, stmt := range block.Stmts {
		ex := fa.alwaysExits(stmt)
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
		hints := []string{"remove it"}
		if ex == exitNever {
			hints = []string{"remove it, or give the loop a `break` that ends it"}
		}
		return TypeError{
			Line:    line,
			Col:     col,
			Message: unreachableMessage(stmt, ex),
			Hints:   hints,
			Code:    UnreachableCodeCode,
		}, true
	}
	return TypeError{}, false
}

// exitSet records which of `return`, `break` and `continue` a statement that
// always exits may leave by, and exitNever when it may instead run a loop
// that never finishes. Zero means the statement does not always exit.
type exitSet uint8

const (
	exitReturn exitSet = 1 << iota
	exitBreak
	exitContinue
	exitNever
)

// alwaysExits answers how stmt always leaves its block, or 0 when some path
// through it falls through to the next statement.
func (fa *FileAnalysis) alwaysExits(stmt ast.Node) exitSet {
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
	case *ast.Call:
		if fa.LoopNeverBreaks(s) {
			return exitNever
		}
	case *ast.Block:
		if s == nil {
			return 0
		}
		for _, inner := range s.Stmts {
			if ex := fa.alwaysExits(inner); ex != 0 {
				return ex
			}
		}
	case *ast.If:
		if s == nil || s.Then == nil || s.Else == nil {
			return 0
		}
		then, els := fa.alwaysExits(s.Then), fa.alwaysExits(s.Else)
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
			ex := fa.alwaysExits(br.Body)
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
	case exitNever:
		verb = "loops forever"
	}
	switch stmt.(type) {
	case *ast.Return:
		return "unreachable code after return"
	case *ast.Break:
		return "unreachable code after break"
	case *ast.Continue:
		return "unreachable code after continue"
	case *ast.Call:
		return "unreachable code after a loop that never breaks"
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

// AlwaysExits reports whether stmt always leaves its block by `return`,
// `break` or `continue`, or never finishes (LoopNeverBreaks), by the rule
// unreachable-code applies. The IR builder asks it of a binding's value:
// nothing after `y = case x { 0 -> continue  _ -> break 1 }` runs, so the
// builder lowers the `case` as the block's tail and lowers nothing after it.
// stmt is a statement of fa's file.
func (fa *FileAnalysis) AlwaysExits(stmt ast.Node) bool { return fa.alwaysExits(stmt) != 0 }

// LoopNeverBreaks reports whether n, a node of fa's file, is a call
// `Iter.loop(...)` whose callback cannot end it: a lambda or a capture whose
// body holds no `break` of its own, or any other function value, a named
// function among them, that is not an iter-sensitive function whose own
// scope breaks (`Iter.loop(step, from: 0)`). A `break` inside a
// lambda or a nested `fn` in the body belongs to that callback, not to this
// loop. In the callback `return v` and `continue` go
// round again, so nothing reaches the loop's exit: a trap, the task's
// cancellation or the end of the process stops it, and no code after it
// runs. The loop still has its state type S, as every `Iter.loop` does; it
// never produces a value of it.
//
// The callee is std's `Iter.loop` written qualified (`Iter` is a reserved
// type name, so `Iter.loop` is always std's) or a bare name whose recorded
// reference resolves to it: `loop` from `import std/iter.Iter.{loop}`, or
// `forever` from `Iter.{loop as forever}`. A bare `loop` that names a local
// function or binding is not one.
func (fa *FileAnalysis) LoopNeverBreaks(n ast.Node) bool {
	call, isCall := n.(*ast.Call)
	if !isCall || call == nil || !fa.isIterLoopCallee(call.Func) {
		return false
	}
	cb, _, positionalSeed := seededCallArgs(call, nil, false)
	if positionalSeed != nil {
		return false
	}
	if lam, ok := cb.(*ast.Lambda); cb == nil || (ok && (lam == nil || lam.Body == nil)) {
		return false
	}
	return !fa.breaksIteration(cb)
}

// isIterLoopCallee reports whether a call's callee is std's `Iter.loop`.
func (fa *FileAnalysis) isIterLoopCallee(callee ast.Node) bool {
	switch f := callee.(type) {
	case *ast.FieldAccess:
		if f == nil || f.Field == nil || f.Field.Name != "loop" {
			return false
		}
		owner, isType := f.Object.(*ast.TypeIdent)
		return isType && owner != nil && owner.Name == "Iter"
	case *ast.Ident:
		if f == nil || fa == nil {
			return false
		}
		sym := resolvedSymbol(fa.References[Pos{Line: f.Line, Col: f.Col}])
		return sym != nil && sym.Kind == SymbolFunction && sym.OwningType == "Iter" && sym.Name == "loop"
	}
	return false
}
