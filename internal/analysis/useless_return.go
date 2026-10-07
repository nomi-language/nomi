package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UselessReturnPrefix starts the error for a bare `return` that ends a
// body; the LSP's "Remove the `return`" fix keys on it.
const UselessReturnPrefix = "this `return` does nothing"

// CheckUselessReturns reports each `return` that changes nothing because the
// body it would leave ends there anyway. The check is syntactic, so it runs
// beside the unused-binding sweep, before types are known.
//
// Two shapes are errors:
//
//   - A bare `return` in tail position of a function, lambda or test body:
//     its body's last statement, reached through the last statement of a
//     block, both branches of an `if` and every arm of a `case`. A bare
//     return after a `dbg` stays legal, and so does one after an assertion
//     outside a test body: those statements have a non-Unit value, and the
//     return is what makes the body Unit.
//   - A tail `if` or `case` whose every branch is empty or a bare `return`,
//     with at least one `return`: the whole statement does nothing.
//
// `return value` at the tail is not an error; `nomi fmt` rewrites it to the
// value. A `concurrent` block, a binding's `else`, a group's `setup` and any
// statement before the last are not tail position.
func CheckUselessReturns(nodes []ast.Node) []TypeError {
	u := uselessReturns{}
	// The derive and Debug passes share type annotations among nodes, so a
	// node can be met twice; each one is checked once.
	seen := map[ast.Node]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if seen[n] {
				return false
			}
			seen[n] = true
			u.visit(n)
			return true
		})
	}
	return u.errs
}

type uselessReturns struct {
	errs []TypeError
}

// visit checks n's own body, when n has one.
func (u *uselessReturns) visit(n ast.Node) {
	switch n := n.(type) {
	case *ast.FuncDef:
		if !n.AutoSynth {
			u.body(n.Body, fmt.Sprintf("`%s`", n.Name), false)
		}
	case *ast.InterfaceMethod:
		if b, ok := n.Body.(*ast.Block); ok {
			u.body(b, fmt.Sprintf("`%s`", n.Name), false)
		}
	case *ast.Lambda:
		u.body(n.Body, "this lambda", false)
	case *ast.TestDecl:
		if !n.Group {
			u.body(n.Body, fmt.Sprintf("test %q", n.Name), true)
		}
	}
}

// tailWalk carries one body's name and kind through its tail.
type tailWalk struct {
	u     *uselessReturns
	owner string
	test  bool
	top   *ast.Block // the body itself
}

func (u *uselessReturns) body(b *ast.Block, owner string, test bool) {
	if b == nil {
		return
	}
	tailWalk{u: u, owner: owner, test: test, top: b}.block(b)
}

func (t tailWalk) block(b *ast.Block) {
	if b == nil || len(b.Stmts) == 0 {
		return
	}
	last := len(b.Stmts) - 1
	switch v := b.Stmts[last].(type) {
	case *ast.Return:
		if v.Value != nil {
			return
		}
		if last > 0 && t.returnMakesUnit(b.Stmts[last-1]) {
			return
		}
		t.report(v, last == 0 && b == t.top)
	case *ast.ExprStmt:
		t.expr(v.Expr)
	default:
		t.expr(v)
	}
}

// returnMakesUnit reports whether a bare return after stmt turns a non-Unit
// value into the body's Unit: a `dbg`, or an assertion outside a test body.
func (t tailWalk) returnMakesUnit(stmt ast.Node) bool {
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	switch v := stmt.(type) {
	case *ast.Dbg:
		return true
	case *ast.Assertion:
		return !t.test
	case *ast.Binary:
		return v.Op == "|>" && t.returnMakesUnit(v.Right)
	}
	return false
}

func (t tailWalk) expr(n ast.Node) {
	switch v := n.(type) {
	case *ast.Block:
		t.block(v)
	case *ast.If:
		if ifDoesNothing(v) {
			t.reportBranching(v.Line, v.Col, "if")
			return
		}
		t.block(v.Then)
		t.expr(v.Else)
	case *ast.Case:
		if caseDoesNothing(v) {
			t.reportBranching(v.Line, v.Col, "case")
			return
		}
		for _, br := range v.Branches {
			if ret, ok := br.Body.(*ast.Return); ok {
				if ret.Value == nil {
					t.report(ret, false)
				}
				continue
			}
			t.expr(br.Body)
		}
	}
}

// report adds the error for a bare return. alone marks one that is its
// body's only statement, which is more likely a stub than a slip.
func (t tailWalk) report(ret *ast.Return, alone bool) {
	if IsSynthesizedLine(ret.Line) {
		return
	}
	hint := "remove it"
	if alone {
		hint = "remove it; if the function isn't written yet, write `todo` instead"
	}
	t.u.errs = append(t.u.errs, TypeError{
		Line:    ret.Line,
		Col:     ret.Col,
		EndLine: ret.Line,
		EndCol:  ret.Col + len("return"),
		Message: fmt.Sprintf("%s: it is the last statement of %s", UselessReturnPrefix, t.owner),
	}.WithHint(hint))
}

func (t tailWalk) reportBranching(line, col int, keyword string) {
	if IsSynthesizedLine(line) {
		return
	}
	t.u.errs = append(t.u.errs, TypeError{
		Line:    line,
		Col:     col,
		EndLine: line,
		EndCol:  col + len(keyword),
		Message: fmt.Sprintf("this `%s` does nothing: every branch returns and nothing follows it", keyword),
	}.WithHint(fmt.Sprintf("remove the `%s`; keep what it tests only if evaluating that has an effect you need", keyword)))
}

// ifDoesNothing reports whether every branch of an `if` chain, a missing
// `else` included, is empty or a bare `return`, and at least one returns.
func ifDoesNothing(n *ast.If) bool {
	returns := false
	for {
		r, ok := branchDoesNothing(n.Then)
		if !ok {
			return false
		}
		returns = returns || r
		switch e := n.Else.(type) {
		case nil:
			return returns
		case *ast.If:
			n = e
		case *ast.Block:
			r, ok := branchDoesNothing(e)
			return ok && (returns || r)
		default:
			return false
		}
	}
}

// caseDoesNothing is ifDoesNothing for the arms of a `case`.
func caseDoesNothing(n *ast.Case) bool {
	if len(n.Branches) == 0 {
		return false
	}
	returns := false
	for _, br := range n.Branches {
		r, ok := branchDoesNothing(br.Body)
		if !ok {
			return false
		}
		returns = returns || r
	}
	return returns
}

// branchDoesNothing reports whether a branch body is empty or a bare
// `return` (ok), and whether it is the return (returns).
func branchDoesNothing(n ast.Node) (returns, ok bool) {
	switch v := n.(type) {
	case nil:
		return false, true
	case *ast.Return:
		return true, v.Value == nil
	case *ast.Block:
		if v == nil || len(v.Stmts) == 0 {
			return false, true
		}
		if len(v.Stmts) == 1 {
			return branchDoesNothing(v.Stmts[0])
		}
	}
	return false, false
}
