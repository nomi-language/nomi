package parser

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// operandShape renders n with every operation parenthesized and its own
// parentheses dropped, so two sources of one shape render alike.
func operandShape(n ast.Node) string {
	switch v := n.(type) {
	case *ast.GroupedExpr:
		return operandShape(v.Expr)
	case *ast.Unary:
		return "(" + v.Op + " " + operandShape(v.Right) + ")"
	case *ast.Binary:
		return "(" + operandShape(v.Left) + " " + v.Op + " " + operandShape(v.Right) + ")"
	case *ast.RangeLit:
		op := ".."
		if v.Inclusive {
			op = "..="
		}
		return "(" + operandShape(v.Start) + " " + op + " " + operandShape(v.End) + ")"
	case *ast.Is:
		return "(" + operandShape(v.Value) + " is P)"
	case *ast.Ascription:
		return "(" + operandShape(v.Value) + " as T)"
	case *ast.Call:
		return "f()"
	case *ast.Ident:
		return v.Name
	}
	return fmt.Sprintf("%T", n)
}

// token.OperandNeedsParens, which `nomi fmt` decides parentheses with, is
// the parser's answer: for every operator, every side and every operator
// an operand can apply last, dropping the operand's parentheses changes
// the parse, or makes it an error, exactly when the predicate says the
// operand needs them.
func TestOperandNeedsParensIsTheParsersAnswer(t *testing.T) {
	// apply writes the operator op over its operands as source: the right
	// operand of `is` is a pattern, of `as` a type and of `|>` a stage.
	apply := func(op, left, right string) string {
		switch op {
		case "is":
			return left + " is Some(_)"
		case "as":
			return left + " as Int"
		case "|>":
			return left + " |> f()"
		case "..", "..=":
			return left + op + right
		}
		return left + " " + op + " " + right
	}
	// hasRight reports whether op's right operand is an expression.
	hasRight := func(op string) bool { return op != "is" && op != "as" && op != "|>" }

	type operand struct{ op, src string }
	var children []operand
	var infix []string
	for _, o := range token.InfixOperators() {
		infix = append(infix, o.Text)
		children = append(children, operand{o.Text, apply(o.Text, "b", "c")})
	}
	children = append(children, operand{"not", "not b"})

	check := func(parent string, side token.Side, child operand, bare, grouped string) {
		t.Helper()
		want := operandShape(parseExpr(t, grouped))
		got := "a parse error"
		if nodes, err := Parse(lexer.Lex(bare)); err == nil && len(nodes) == 1 {
			if stmt, ok := nodes[0].(*ast.ExprStmt); ok {
				got = operandShape(stmt.Expr)
			}
		}
		changes := got != want
		if needs := token.OperandNeedsParens(parent, side, child.op); needs != changes {
			t.Errorf("OperandNeedsParens(%q, %d, %q) = %v, and without the parentheses `%s` parses as %s, with them `%s` as %s",
				parent, side, child.op, needs, bare, got, grouped, want)
		}
	}

	for _, parent := range infix {
		for _, child := range children {
			check(parent, token.LeftOperand, child,
				apply(parent, child.src, "z"), apply(parent, "("+child.src+")", "z"))
			if hasRight(parent) {
				check(parent, token.RightOperand, child,
					apply(parent, "a", child.src), apply(parent, "a", "("+child.src+")"))
			}
		}
	}
	for _, parent := range []string{"-", "~~~", "not"} {
		prefix := parent
		if parent == "not" {
			prefix = "not "
		}
		for _, child := range children {
			check(parent, token.PrefixOperand, child, prefix+child.src, prefix+"("+child.src+")")
		}
	}
}
