package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnreachableCodeCode tags the diagnostic for a statement that follows a
// statement that always exits its block.
const UnreachableCodeCode = "unreachable-code"

// checkUnreachableCode reports, in each block of the file's nodes, the
// first statement that follows a statement that always exits
// (checkUnreachable). It runs once the file's bodies are checked, since
// whether a call never returns is the call's record (NeverReturns). A block
// is reported after the blocks it holds.
func (c *checker) checkUnreachableCode(nodes []ast.Node) {
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		ast.Children(n, walk)
		if block, ok := n.(*ast.Block); ok {
			if te, found := checkUnreachable(c.fa, block); found {
				c.report(te)
			}
		}
	}
	for _, n := range nodes {
		walk(n)
	}
}

// checkUnreachable reports the first statement of block that follows a
// statement that always exits: a `return`, `break` or `continue`; an
// `unreachable`, which always traps; a call that never returns
// (NeverReturns: an `Iter.loop` whose callback holds no `break`), which
// never finishes; an `if` with an `else` whose every branch always exits; a
// `case` whose every arm always exits (a `case` is exhaustive, so one of
// its arms runs); a block statement holding a statement that always exits;
// or a statement that always evaluates an operand that does (leaves), a
// binding of a loop that never breaks among them. Nothing after such a
// statement can run, and the IR builder would otherwise have to lower dead
// code. It is an error, as an unused binding is: the analyzer has no warning
// class.
//
// Two things that stop a body are not exits here. `todo` is a statement of
// type Unit, so code after it stays legal while a function is being stubbed
// out. No call but a loop that never breaks counts: no Nomi function is
// known not to return.
func checkUnreachable(fa *FileAnalysis, block *ast.Block) (TypeError, bool) {
	if block == nil {
		return TypeError{}, false
	}
	for i, stmt := range block.Stmts {
		ex := fa.leaves(stmt, true)
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
			Message: unreachableMessage(stmt, ex, fa.leavingRegion(stmt, true) != 0),
			Hints:   hints,
			Code:    UnreachableCodeCode,
		}, true
	}
	return TypeError{}, false
}

// exitSet records which of `return`, `break` and `continue` a statement that
// always exits may leave by, exitNever when it may instead run a loop that
// never finishes, and exitTrap when it may reach an `unreachable`. Zero means
// the statement does not always exit.
type exitSet uint8

const (
	exitReturn exitSet = 1 << iota
	exitBreak
	exitContinue
	exitNever
	exitTrap
)

// leaves answers how stmt always leaves its block, or 0 when some path
// through it falls through to the next statement. It is the one answer to
// that question: the unreachable-code rule asks it of every statement, and
// the checker asks it of a block's last statement (blockResult). A
// statement leaves when it is a region that leaves (leavingRegion), or
// when it always evaluates an operand that leaves, as `close(if c then
// return 1 else return 2)`, `x = f(case c ... end)` with every arm a
// `return`, and a `defer` whose call has such an argument do. Which
// children a node always evaluates is ast.EvaluatedOperands, the table the
// IR builder lifts such a region by (irbuild, exitingOperand).
//
// traps says whether an `unreachable` statement counts as leaving: it does
// for the unreachable-code rule, and not for the type of a block or for
// the IR builder, which lowers it as an operand that traps, as it lowers
// `todo`. An `unreachable` that is an operand never counts: `x: T =
// unreachable "..."` is a binding, as `x: T = todo` is, and code after it
// stays legal.
func (fa *FileAnalysis) leaves(stmt ast.Node, traps bool) exitSet {
	leavesCount()
	if ast.IsNilNode(stmt) {
		return 0
	}
	if es, ok := stmt.(*ast.ExprStmt); ok {
		// leavingRegion looks through an expression statement, so asking
		// it and then the expression, the statement's one operand, would
		// ask the expression twice, and double the work at each level of
		// nesting.
		return fa.leaves(es.Expr, traps)
	}
	if ex := fa.leavingRegion(stmt, traps); ex != 0 {
		return ex
	}
	for _, operand := range ast.EvaluatedOperands(stmt) {
		if ex := fa.leaves(operand, false); ex != 0 {
			return ex
		}
	}
	return 0
}

// leavingRegion answers how n leaves when n is itself what leaves, and 0
// when it is not: a `return`, `break` or `continue`, an `unreachable` when
// traps is set, an `Iter.loop` that never breaks, a block holding a
// statement that leaves, an `if` with an `else` whose every branch leaves,
// or a `case` whose every arm does. Whether a statement or a branch inside
// it leaves is leaves's answer. leaves and AlwaysExits call it, and the
// unreachable-code rule to tell a region from a statement that leaves
// through an operand.
func (fa *FileAnalysis) leavingRegion(n ast.Node, traps bool) exitSet {
	if es, ok := n.(*ast.ExprStmt); ok {
		n = es.Expr
	}
	switch s := n.(type) {
	case *ast.Return:
		return exitReturn
	case *ast.Break:
		return exitBreak
	case *ast.Continue:
		return exitContinue
	case *ast.Unreachable:
		if traps {
			return exitTrap
		}
	case *ast.Call:
		if fa.NeverReturns(s) {
			return exitNever
		}
	case *ast.Block:
		if s == nil {
			return 0
		}
		for _, inner := range s.Stmts {
			if ex := fa.leaves(inner, traps); ex != 0 {
				return ex
			}
		}
	case *ast.If:
		if s == nil || s.Then == nil || s.Else == nil {
			return 0
		}
		then, els := fa.leaves(s.Then, traps), fa.leaves(s.Else, traps)
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
			ex := fa.leaves(br.Body, traps)
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
// isRegion says the statement is itself what leaves (leavingRegion); one
// that leaves through an operand is named only as the statement above.
func unreachableMessage(stmt ast.Node, ex exitSet, isRegion bool) string {
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
	case exitTrap:
		verb = "traps"
	}
	if !isRegion {
		return fmt.Sprintf("unreachable code: the statement above always %s", verb)
	}
	switch stmt.(type) {
	case *ast.Return:
		return "unreachable code after return"
	case *ast.Break:
		return "unreachable code after break"
	case *ast.Continue:
		return "unreachable code after continue"
	case *ast.Unreachable:
		return "unreachable code after `unreachable`"
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

// AlwaysExits reports whether stmt is itself a region that always leaves
// its block by `return`, `break` or `continue`, or never finishes
// (leavingRegion), by the rule unreachable-code applies. The IR builder
// asks it of an `if` or a `case` it found among a statement's operands
// (irbuild, exitingOperand): it lowers that region as the block's tail and
// drops the construct that would read it, so it needs the region, where
// the checker needs only whether the statement leaves (leaves). stmt is a
// node of fa's file.
func (fa *FileAnalysis) AlwaysExits(stmt ast.Node) bool { return fa.leavingRegion(stmt, false) != 0 }

// NeverReturns reports whether n, a node of fa's file, is a call the
// checker recorded as one that never returns (ResolvedCall.NeverReturns).
// The unreachable-code rule, a block's type and the IR builder read it.
func (fa *FileAnalysis) NeverReturns(n ast.Node) bool {
	call, isCall := n.(*ast.Call)
	if !isCall || call == nil || fa == nil {
		return false
	}
	rc := fa.Calls[call]
	return rc != nil && rc.NeverReturns
}

// loopNeverBreaks decides, as the call is checked (recordPlacement),
// whether call is std's `Iter.loop` and its callback cannot end it: a
// lambda or a capture whose body holds no `break` of its own, or any other
// function value, a named function among them, that is not an
// iter-sensitive function whose own scope breaks (`Iter.loop(0, step)`). A
// `break` inside a lambda or a nested `fn` in the body belongs to that
// callback, not to this loop. In the callback `return v` and `continue` go
// round again, so nothing reaches the loop's exit: a trap, the task's
// cancellation or the end of the process stops it, and no code after it
// runs. The loop still has its state type S, as every `Iter.loop` does; it
// never produces a value of it.
//
// The callee is std's `Iter.loop` by the declaration it reaches
// (stdFuncNamed), however it is written: `Iter.loop`, `Seq.loop` under
// `import std::iter.{Iter as Seq}`, or a bare `loop` or `forever` from
// `import std::iter.Iter.{loop}` or `Iter.{loop as forever}`. A program's
// own type imported as `Iter` has a `loop` that is not one.
func (c *checker) loopNeverBreaks(call *ast.Call) bool {
	if c.fa.stdFuncNamed(call.Func) != iterOwnerFunc("loop") {
		return false
	}
	cb, _ := loopCallArgs(call, nil)
	if lam, ok := cb.(*ast.Lambda); cb == nil || (ok && (lam == nil || lam.Body == nil)) {
		return false
	}
	return !c.fa.breaksIteration(cb)
}
