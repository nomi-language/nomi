package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/parser"
)

// A pipe stage is a call. `x |> f(a)` calls `f(x, a)`: the pipe inserts the
// piped value as the first argument of the call that follows it. A name
// without parentheses is a function reference wherever it stands
// (`Iter.map(xs, io.print)` passes io.print, it does not call it), so a
// bare name is not a stage: `3 |> double`, `x |> io.print`, `x |> Ok` and
// `x |> Type.func` are errors that name the call to write.
//
// The stages that are not calls are the keyword stages (`|> dbg`,
// a `?` stage, `|> todo`, `|> if`, `|> case`, `|> then fn v -> ...`), which
// checkBinary recognizes before it reaches reportBarePipeStage. A field
// accessor (`user |> .name`) has its own error
// (fieldAccessorPipeStageMessage), and so does a bare lambda
// (`x |> fn v -> ...`, reportLambdaPipeStage): a lambda's body runs to the end
// of its expression, so `then` is what applies one to the piped value.

// isPipeKeywordStage reports the keyword stages checkBinary judges after
// the stage dispatch: `x |> todo`, and `x |> dbg expr`, which it rejects.
func isPipeKeywordStage(fa *FileAnalysis, stage ast.Node) bool {
	switch fa.AsTodo(ungroupExpr(stage)).(type) {
	case *ast.Todo, *ast.Unreachable, *ast.Dbg:
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

// barePipeStagePrefix begins every diagnostic reportBarePipeStage
// reports.
const barePipeStagePrefix = "a pipe stage is a call"

// BarePipeStageCode tags the error for a pipe stage that is a name and not
// a call (`3 |> double`).
const BarePipeStageCode = "bare-pipe-stage"

// LambdaPipeStageCode tags the error for a lambda piped into
// (`x |> fn v -> v + 1`).
const LambdaPipeStageCode = "lambda-pipe-stage"

// reportBarePipeStage reports a stage that is not a call. When the stage is
// a name written on its own, the error carries the fix that appends `()`.
func (c *checker) reportBarePipeStage(stage ast.Node) {
	inner := ungroupExpr(stage)
	var msg, call string
	if name, ok := PipeStageName(inner); ok {
		msg = fmt.Sprintf("%s: write `%s()`", barePipeStagePrefix, name)
		call = name
	} else if dv, ok := inner.(*ast.DotVariant); ok {
		msg = fmt.Sprintf("%s: write the variant through its enum, as in `Enum.%s()`", barePipeStagePrefix, dv.Name)
	} else {
		msg = fmt.Sprintf("%s, such as `f()`, or a keyword stage (`dbg`, `if`, `case`, `then`)", barePipeStagePrefix)
	}
	e := errAt(stage, msg)
	e.Code = BarePipeStageCode
	if e.Line == 0 {
		line, col := exprStartLineCol(stage)
		e.Line, e.Col = line, col
	}
	// The span is the name as written when it is as long as the name: a
	// stage in parentheses, or a name with spaces in it, gets no fix.
	if call != "" && e.EndLine == e.Line && e.EndCol-e.Col == len(call) {
		e.Fix = parser.Replace("Call `"+call+"()`", e.EndLine, e.EndCol, e.EndLine, e.EndCol, "()")
	}
	c.report(e.WithHint("the pipe passes its value as the first argument of the call after `|>`; a name without parentheses is a function reference"))
}

// lambdaPipeStagePrefix begins the diagnostic reportLambdaPipeStage
// reports.
const lambdaPipeStagePrefix = "a lambda is not a pipe stage"

// reportLambdaPipeStage reports `x |> fn v -> ...`. A lambda's body runs to
// the end of its expression, so the lambda is not a stage; `then fn v -> ...`
// is. stage is the lambda as the pipe holds it: when it is the lambda
// itself, not one in parentheses, the error carries the fix that writes
// `then ` before it.
func (c *checker) reportLambdaPipeStage(stage ast.Node, lam *ast.Lambda) {
	msg := fmt.Sprintf("%s: write `then %s ...`", lambdaPipeStagePrefix, lambdaHeaderText(lam))
	e := errAt(lam, msg)
	e.Code = LambdaPipeStageCode
	if stage == ast.Node(lam) && e.Line > 0 {
		e.Fix = parser.Replace("Write `then` before the lambda", e.Line, e.Col, e.Line, e.Col, "then ")
	}
	c.report(e.WithHint("`then` applies a lambda to the piped value; a named function, called as `|> f()`, is the other way to write the step"))
}

// reportCapturePipeStage reports `x |> &f(&1)`. A stage is a call the pipe
// fills at its first argument; `then &f(&1)` applies a capture.
func (c *checker) reportCapturePipeStage(n *ast.Capture) {
	c.report(errAt(n, fmt.Sprintf("a capture is not a pipe stage: write `then %s`", format.RenderNode(n))).
		WithHint("`then` applies a capture or a lambda to the piped value"))
}

// lambdaHeaderText is lam's parameter list, `fn v ->` or `fn a, b ->`, with
// `fn v ->` standing in for a list that holds a pattern.
func lambdaHeaderText(lam *ast.Lambda) string {
	names := make([]string, 0, len(lam.Params))
	for _, p := range lam.Params {
		if p.Destructure != nil || p.Name == "" {
			return "fn v ->"
		}
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "fn ->"
	}
	return "fn " + strings.Join(names, ", ") + " ->"
}
