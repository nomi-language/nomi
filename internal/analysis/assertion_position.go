package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// rejectValueAssertions reports each `assert` or `refute` written where
// nothing may hold one. An assertion stands as its own statement, as a
// binding's value (`ok = assert x`) or as a case arm's body (spec §36,
// Assertions). Anywhere else its value would feed another expression: the
// subject of another assertion (`assert assert x`), a call's argument, a
// collection element, a parenthesized expression, a condition, a returned
// value, the head of a pipe. Those are errors at the keyword.
//
// The positions other rules already report are left to them: an operand of
// `!` or of a binary operator (rejectAssertionOperand), the right of `|>`
// (`must be placed at the head of the pipeline`), and a default or `once`
// initializer, which has no function to exit (rejectExitlessExit).
func (c *checker) rejectValueAssertions(nodes []ast.Node) {
	allowed := map[*ast.Assertion]bool{}
	// subjectOf maps an assertion written as another's subject to that one.
	subjectOf := map[*ast.Assertion]*ast.Assertion{}
	allow := func(n ast.Node) {
		if a, ok := ungroupExpr(n).(*ast.Assertion); ok {
			allowed[a] = true
		}
	}
	// allowStmt admits a statement or binding value only when it is the
	// assertion itself: `ok = (assert x)` parenthesizes it into an
	// expression.
	allowStmt := func(n ast.Node) {
		if a, ok := n.(*ast.Assertion); ok {
			allowed[a] = true
		}
	}
	for _, n := range nodes {
		allowStmt(n)
	}
	for _, root := range nodes {
		ast.Inspect(root, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Assertion:
				if inner, ok := ungroupExpr(v.Expr).(*ast.Assertion); ok {
					subjectOf[inner] = v
				}
				if !v.Check && !allowed[v] {
					c.report(valueAssertionError(v, subjectOf[v]))
				}
			case *ast.Block:
				for _, stmt := range v.Stmts {
					allowStmt(stmt)
				}
			case *ast.ExprStmt:
				allowStmt(v.Expr)
			case *ast.Binding:
				allowStmt(v.Value)
			case *ast.PatternBinding:
				allowStmt(v.Value)
			case *ast.TupleDestructure:
				allowStmt(v.Value)
			case *ast.StructDestructure:
				allowStmt(v.Value)
			case *ast.MapDestructure:
				allowStmt(v.Value)
			case *ast.DistinctDestructure:
				allowStmt(v.Value)
			case *ast.PatternDestructure:
				// `assert P = assert v` is an assertion's subject; a plain
				// `P = assert v` is a binding's value.
				if v.AssertLine == 0 {
					allowStmt(v.Value)
				}
			case *ast.Case:
				for _, br := range v.Branches {
					allowStmt(br.Body)
				}
			case *ast.Unary:
				allow(v.Right)
			case *ast.Binary:
				if v.Op != "|>" {
					allow(v.Left)
				}
				allow(v.Right)
			case *ast.FuncDef, *ast.Lambda, *ast.OnceBinding, *ast.StructDef, *ast.EnumDef:
				// A parameter, field or variant default and a `once`
				// initializer: rejectExitlessExit reports an assertion there.
				ast.Children(n, allow)
			}
			return true
		})
	}
}

// valueAssertionError is the error at an assertion written as a value. outer
// is the assertion whose subject it is, or nil.
func valueAssertionError(a, outer *ast.Assertion) TypeError {
	kw := assertionKeyword(a)
	e := TypeError{
		Line:    a.Line,
		Col:     a.Col,
		Message: fmt.Sprintf("`%s` cannot be used as a value here: an assertion stands as its own statement or as a binding's value", kw),
	}
	if outer != nil {
		return e.WithHint(fmt.Sprintf("remove the outer `%s`: it would check the value this `%s` passes on, not a condition",
			assertionKeyword(outer), kw))
	}
	return e.WithHint("write the assertion on its own line, or bind its value first: `ok = " + kw + " condition`")
}
