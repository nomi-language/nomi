package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// checkTapStage checks the stage `x |> tap fn v -> body` (or `tap &f(&1)`, or
// `tap f`), whose piped value has type left. The function takes the piped
// value and returns Unit, and the stage answers the piped value itself. A
// body ending in a `dbg` observation returns Unit, as a Unit function body
// ending in one does.
func (c *checker) checkTapStage(pipe *ast.Binary, n *ast.Tap, left Type) Type {
	expectedFT := &FuncType{Params: []Type{left}, Return: TypeUnit}
	errsBefore := len(c.errors)
	// A body that already failed to check (a `?` with nothing to
	// propagate to, a mismatched `return`) is not also reported here.
	var lamTy Type
	switch {
	case n.Capture != nil:
		lamTy = c.checkCapture(n.Capture, expectedFT)
	case n.Name != nil:
		// A function by name is checked as a function value passed where
		// a one-parameter function is expected: a generic one is
		// instantiated from the piped value, and one whose parameters past
		// the first have defaults is that one-parameter function
		// (defaultFilledFuncValue).
		lamTy = c.checkNodeExpecting(n.Name, expectedFT)
		if len(c.errors) != errsBefore || !c.checkTapNameTakes(pipe, n, left, lamTy) {
			c.registerControlFlowHover(n.Line, n.Col, "tap", left, left, left != nil, false)
			return left
		}
	default:
		c.checkPipeLambdaArity(n.Lambda, "tap")
		lamTy = c.checkLambdaExpecting(n.Lambda, expectedFT)
	}
	// Recorded as an argument's lambda is, for the hover on its `fn`.
	c.fa.recordExprType(n.Fn(), lamTy)
	if ft, ok := lamTy.(*FuncType); ok && n.Capture != nil && len(ft.Params) > 0 {
		c.checkThenCaptureTakes(pipe, n.Capture, left, ft.Params[0])
	}
	if ft, ok := lamTy.(*FuncType); ok && len(c.errors) == errsBefore {
		c.checkTapReturnsUnit(n, ft.Return)
	}
	c.registerControlFlowHover(n.Line, n.Col, "tap", left, left, left != nil, false)
	return left
}

// checkTapNameTakes reports a `tap f` stage whose f is no function of one
// parameter that takes the piped value, of type left; ty is f's type. It
// answers whether f is such a function.
func (c *checker) checkTapNameTakes(pipe *ast.Binary, n *ast.Tap, left, ty Type) bool {
	if ty == nil || left == nil {
		return false
	}
	name := funcValueName(n.Name)
	ft, ok := resolveTypeVar(ty).(*FuncType)
	if !ok {
		c.report(errAt(n.Name, c.typef("`tap` takes a function, but `%s` is %s", name, withArticle(c.typef("%s", ty)))).
			WithHint("write `tap fn v -> ...` to run an expression on the piped value"))
		return false
	}
	if len(ft.Params) != 1 {
		c.report(errAt(n.Name, fmt.Sprintf(
			"a `tap` function takes one parameter, the piped value; `%s` takes %d", name, len(ft.Params))).
			WithHint(c.defaultedFuncValueHint(&FuncType{Params: []Type{left}, Return: TypeUnit}, ft, n.Name)))
		return false
	}
	if !c.argMatchesParam(left, ft.Params[0], c.recPos(pipe.Line, pipe.Col), RecordingKindInterfaceTypedParam) {
		wantS, haveS := c.typePair(ft.Params[0], left)
		e := errAt(n.Name, fmt.Sprintf("`%s` takes %s, but the pipe passes %s", name, withArticle(wantS), withArticle(haveS)))
		if r, ok := relatedAt((&pipeHead{pipe: pipe}).source(), c.typef("the piped value is %s", withArticle(c.typef("%s", left)))); ok {
			e = e.WithRelated(r)
		}
		c.report(e.WithHint(c.pipeHeadHint(ft.Params[0], left, ungroupExpr(pipe.Left))))
		return false
	}
	return true
}

// checkTapReturnsUnit reports a `tap` function whose body answers a value
// other than Unit: the stage discards it, so the author most likely meant
// `then`, or for a function by name a call stage.
func (c *checker) checkTapReturnsUnit(n *ast.Tap, ret Type) {
	if ret == nil || isUnitLike(ret) || resolveTypeVar(ret) == TypeInfallible {
		return
	}
	if _, open := resolveTypeVar(ret).(*TypeVar); open {
		return
	}
	at := n.Fn()
	switch {
	case n.Name != nil:
		name := funcValueName(n.Name)
		c.errors = append(c.errors, errAt(at, c.typef(
			"`tap` passes its input on unchanged, so its function must return Unit; `%s` returns %s", name, ret)).
			WithHint(fmt.Sprintf("use `|> %s()` to replace the value with the function's result", name)))
		return
	case n.Capture != nil:
		if EndsInDbg(n.Capture.Body) {
			return
		}
	default:
		if body := n.Lambda.Body; body != nil && len(body.Stmts) > 0 {
			if es, ok := body.Stmts[len(body.Stmts)-1].(*ast.ExprStmt); ok {
				if EndsInDbg(es.Expr) {
					return
				}
				at = es.Expr
			}
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
