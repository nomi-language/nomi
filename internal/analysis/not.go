package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A `not` pipe stage, `x |> not f()`, negates the stage's result: the pipe
// is checked as `x |> f()`, whose type must be Bool, and its type is Bool.

// checkNotStage checks a pipe whose stage is negated.
func (c *checker) checkNotStage(n *ast.Binary, stage *ast.Unary) Type {
	plain := *n
	plain.Right = stage.Right
	operand := c.checkBinary(&plain, nil)
	c.fa.recordExprType(stage, TypeBool)
	if operand == nil {
		return nil
	}
	if !TypesEqual(operand, TypeBool) {
		c.report(TypeError{Line: stage.Line, Col: stage.Col,
			Message: c.typef("`not` takes a Bool, and `%s` is %s", UnwrapSource(&plain), operand)})
	}
	return TypeBool
}
