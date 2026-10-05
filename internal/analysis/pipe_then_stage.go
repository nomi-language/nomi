package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// A `then` stage's bare body ends at the next `|>`, so in
//
//	xs |> then |v| v |> Iter.filter(|x| x > Iter.count(v))
//
// `Iter.filter(...)` is the next stage of the outer pipeline and `v` is not
// in scope there. The plain "undefined variable" error does not say why, so
// while the checker checks a pipe stage it keeps the parameters of the
// bare-bodied `then` stages before it, and an unbound name that is one of
// them gets an error naming the boundary.

// pipeLambdaParam is a parameter of a bare-bodied `then` stage earlier in a
// pipeline the checker is inside.
type pipeLambdaParam struct {
	name string
	line int // the `then` stage's line
}

// pushPipeLambdaParams records the parameters of the bare-bodied `then`
// stages in left, the pipeline before a stage, and returns the function that
// forgets them once the stage is checked.
func (c *checker) pushPipeLambdaParams(left ast.Node) func() {
	mark := len(c.pipeLambdaParams)
	for {
		b, ok := left.(*ast.Binary)
		if !ok || b.Op != "|>" {
			break
		}
		if then, ok := b.Right.(*ast.Then); ok && bareLambdaBody(then.Lambda) {
			for _, p := range then.Lambda.Params {
				if p.Destructure == nil && p.Name != "" {
					c.pipeLambdaParams = append(c.pipeLambdaParams, pipeLambdaParam{name: p.Name, line: then.Line})
				}
			}
		}
		left = b.Left
	}
	return func() { c.pipeLambdaParams = c.pipeLambdaParams[:mark] }
}

// bareLambdaBody reports whether lam's body is a single expression rather
// than a `{ ... }` block. The parser wraps a bare body in a synthesized
// Block, which has no closing line.
func bareLambdaBody(lam *ast.Lambda) bool {
	return lam.Body != nil && lam.Body.EndLine == 0
}

// pipeLambdaBoundaryError is the error for n when its name is unbound and is
// the parameter of an earlier `then` stage of the pipeline being checked.
// ok is false when it is not.
func (c *checker) pipeLambdaBoundaryError(n *ast.Ident) (TypeError, bool) {
	for i := len(c.pipeLambdaParams) - 1; i >= 0; i-- {
		p := c.pipeLambdaParams[i]
		if p.name == n.Name {
			msg := fmt.Sprintf("'%s' is the parameter of the `then` stage on line %d, whose body ends at the next `|>`", n.Name, p.line)
			return errAt(n, msg).WithHint(fmt.Sprintf("write `then |%s| { ... }` to keep the pipe inside it", n.Name)), true
		}
	}
	return TypeError{}, false
}
