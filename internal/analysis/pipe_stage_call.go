package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A pipe stage is a call. `x |> f(a)` calls `f(x, a)`: the pipe inserts the
// piped value as the first argument of the call that follows it. A name
// without parentheses is a function reference wherever it stands
// (`Iter.map(xs, io.print)` passes io.print, it does not call it), so a
// bare name is not a stage: `3 |> double`, `x |> io.print`, `x |> Ok` and
// `x |> Type.func` are errors that name the call to write.
//
// The stages that are not calls are the keyword stages (`|> dbg`,
// `|> try`, `|> todo`, `|> if`, `|> case`, `|> then |v| ...`), which
// checkBinary recognizes before it reaches reportBarePipeStage. A field
// accessor (`user |> .name`) has its own error
// (fieldAccessorPipeStageMessage), and so does a bare lambda
// (`x |> |v| ...`, reportLambdaPipeStage): a lambda's body runs to the end
// of its expression, so `then` is what applies one to the piped value.

// isPipeKeywordStage reports the keyword stages checkBinary judges after
// the stage dispatch: `x |> todo`, and `x |> dbg expr`, which it rejects.
func isPipeKeywordStage(stage ast.Node) bool {
	switch ungroupExpr(stage).(type) {
	case *ast.Todo, *ast.Dbg:
		return true
	}
	return false
}

// PipeStageName is the dotted name a bare pipe stage spells (`double`,
// `io.print`, `Shape.area`), and false for any other expression. The LSP's
// quick fix appends `()` to exactly these stages.
func PipeStageName(stage ast.Node) (string, bool) {
	switch v := stage.(type) {
	case *ast.Ident:
		return v.Name, true
	case *ast.TypeIdent:
		return v.Name, true
	case *ast.FieldAccess:
		if v.Field == nil {
			return "", false
		}
		obj, ok := PipeStageName(v.Object)
		if !ok {
			return "", false
		}
		return obj + "." + v.Field.Name, true
	}
	return "", false
}

// BarePipeStagePrefix begins every diagnostic reportBarePipeStage
// reports. The LSP keys its quick fix on it.
const BarePipeStagePrefix = "a pipe stage is a call"

func (c *checker) reportBarePipeStage(stage ast.Node) {
	inner := ungroupExpr(stage)
	var msg string
	if name, ok := PipeStageName(inner); ok {
		msg = fmt.Sprintf("%s: write `%s()`", BarePipeStagePrefix, name)
	} else if dv, ok := inner.(*ast.DotVariant); ok {
		msg = fmt.Sprintf("%s: write the variant through its enum, as in `Enum.%s()`", BarePipeStagePrefix, dv.Name)
	} else {
		msg = fmt.Sprintf("%s, such as `f()`, or a keyword stage (`dbg`, `try`, `if`, `case`, `then`)", BarePipeStagePrefix)
	}
	e := errAt(stage, msg)
	if e.Line == 0 {
		line, col := exprStartLineCol(stage)
		e.Line, e.Col = line, col
	}
	c.report(e.WithHint("the pipe passes its value as the first argument of the call after `|>`; a name without parentheses is a function reference"))
}

// LambdaPipeStagePrefix begins the diagnostic reportLambdaPipeStage
// reports. The LSP keys its quick fix, which inserts `then `, on it.
const LambdaPipeStagePrefix = "a lambda is not a pipe stage"

// reportLambdaPipeStage reports `x |> |v| ...`. A lambda's body runs to the
// end of its expression, so the lambda is not a stage; `then |v| ...` is.
func (c *checker) reportLambdaPipeStage(lam *ast.Lambda) {
	msg := fmt.Sprintf("%s: write `then %s ...`", LambdaPipeStagePrefix, lambdaHeaderText(lam))
	c.report(errAt(lam, msg).WithHint("`then` applies a lambda to the piped value; a named function, called as `|> f()`, is the other way to write the step"))
}

// lambdaHeaderText is lam's parameter list, `|v|` or `|a, b|`, with `|v|`
// standing in for a list that holds a pattern.
func lambdaHeaderText(lam *ast.Lambda) string {
	names := make([]string, 0, len(lam.Params))
	for _, p := range lam.Params {
		if p.Destructure != nil || p.Name == "" {
			return "|v|"
		}
		names = append(names, p.Name)
	}
	return "|" + strings.Join(names, ", ") + "|"
}
