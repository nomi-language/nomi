package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// A postfix `!`, `expr!`: expr's type must be a `Result<T, E>` or a
// `Maybe<T>`, and the `!`'s type is T. An Err or a None traps at run time
// (internal/irbuild's unwrap.go).
//
// As a pipe stage, `x |> f()!`, the `!` unwraps the stage's result: the pipe
// is checked as `x |> f()`, then unwrapped.

// checkUnwrap checks `expr!`.
func (c *checker) checkUnwrap(n *ast.Unwrap) Type {
	return c.unwrapBang(n, n.Expr, c.checkNode(n.Expr))
}

// checkUnwrapStage checks a pipe whose stage is unwrapped, `x |> f()!`.
func (c *checker) checkUnwrapStage(n *ast.Binary, stage *ast.Unwrap) Type {
	plain := *n
	plain.Right = stage.Expr
	return c.unwrapBang(stage, &plain, c.checkBinary(&plain, nil))
}

// unwrapBang is the type of `!` n over expr, whose type is ty: the payload
// of a Result's Ok or a Maybe's Some. Anything else is an error at the `!`.
func (c *checker) unwrapBang(n *ast.Unwrap, expr ast.Node, ty Type) Type {
	if ty == nil {
		return nil
	}
	if et, ok := resolveTypeVar(ty).(*EnumType); ok && isTryOperandName(et.Name) && len(et.TypeArgs) > 0 {
		if n.Line > 0 {
			c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &Symbol{
				Name:  "!",
				Kind:  SymbolTryOp,
				Pos:   Pos{Line: n.Line, Col: n.Col},
				Span:  1,
				TryOp: &TryOpInfo{InputTy: et, Bang: true},
			}
		}
		return et.TypeArgs[0]
	}
	source := UnwrapSource(expr)
	e := TypeError{Line: n.Line, Col: n.Col, Message: c.typef("`!` unwraps a Result or a Maybe, and `%s` is %s", source, ty)}
	if TypesEqual(resolveTypeVar(ty), TypeBool) {
		e = e.WithHint("to stop the program when `" + source + "` is False, write `assert " + source + "`")
	} else {
		e = e.WithHint("remove the `!`: the value is not wrapped")
	}
	c.report(e)
	return nil
}

// UnwrapSource is the expression a `!` unwraps, as the program wrote it, on
// one line: the text a failed `!` reports and a type error quotes.
func UnwrapSource(expr ast.Node) string {
	text := format.RenderNode(expr)
	if !strings.Contains(text, "\n") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " ")
}
