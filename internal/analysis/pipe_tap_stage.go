package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// checkTapStage checks the stage `x |> tap |v| body`, whose piped value has
// type left. The lambda takes the piped value and returns Unit, and the stage
// answers the piped value itself. A body ending in a `dbg` observation
// returns Unit, as a Unit function body ending in one does.
func (c *checker) checkTapStage(n *ast.Tap, left Type) Type {
	c.checkPipeLambdaArity(n.Lambda, "tap")
	expectedFT := &FuncType{Params: []Type{left}, Return: TypeUnit}
	errsBefore := len(c.errors)
	// A body that already failed to check (a `try` with nothing to
	// propagate to, a mismatched `return`) is not also reported here.
	if ft, ok := c.checkLambdaExpecting(n.Lambda, expectedFT).(*FuncType); ok && len(c.errors) == errsBefore {
		c.checkTapReturnsUnit(n, ft.Return)
	}
	c.registerControlFlowHover(n.Line, n.Col, "tap", left, left, left != nil, false)
	return left
}

// checkTapReturnsUnit reports a `tap` lambda whose body answers a value other
// than Unit: the stage discards it, so the author most likely meant `then`.
func (c *checker) checkTapReturnsUnit(n *ast.Tap, ret Type) {
	if ret == nil || isUnitLike(ret) || resolveTypeVar(ret) == TypeInfallible {
		return
	}
	if _, open := resolveTypeVar(ret).(*TypeVar); open {
		return
	}
	var at ast.Node = n.Lambda
	if body := n.Lambda.Body; body != nil && len(body.Stmts) > 0 {
		if es, ok := body.Stmts[len(body.Stmts)-1].(*ast.ExprStmt); ok {
			if EndsInDbg(es.Expr) {
				return
			}
			at = es.Expr
		}
	}
	c.errors = append(c.errors, errAt(at, c.typef(
		"`tap` passes its input on unchanged, so its lambda must return Unit; got %s", ret)).
		WithHint("use `then` to replace the value with the lambda's result"))
}

// checkPipeLambdaArity reports a `then` or `tap` lambda that does not take
// exactly one parameter, the piped value.
func (c *checker) checkPipeLambdaArity(lam *ast.Lambda, keyword string) {
	if len(lam.Params) == 1 {
		return
	}
	c.addError(lam.Line, lam.Col, fmt.Sprintf(
		"a `%s` lambda takes one parameter, the piped value; this one takes %d", keyword, len(lam.Params)))
}
