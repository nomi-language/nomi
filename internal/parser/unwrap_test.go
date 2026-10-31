package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// shape renders n with each Unwrap as `(...)!`, so a test reads what the
// `!` binds to.
func shape(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Unwrap:
		return "(" + shape(v.Expr) + ")!"
	case *ast.FieldAccess:
		return shape(v.Object) + "." + v.Field.Name
	case *ast.Unary:
		return v.Op + shape(v.Right)
	case *ast.Binary:
		return "(" + shape(v.Left) + " " + v.Op + " " + shape(v.Right) + ")"
	case *ast.Call:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = shape(a)
		}
		return shape(v.Func) + "(" + strings.Join(args, ", ") + ")"
	case *ast.GroupedExpr:
		return "[" + shape(v.Expr) + "]"
	case *ast.Ident:
		return v.Name
	case *ast.TypeIdent:
		return v.Name
	case *ast.IntLit:
		return fmt.Sprint(v.Value)
	case *ast.StringLit:
		return fmt.Sprintf("%q", v.Value)
	}
	return fmt.Sprintf("%T", n)
}

// A postfix `!` binds as tightly as a call or a field access, to the operand
// written right before it.
func TestUnwrap_BindsToTheOperandBeforeIt(t *testing.T) {
	cases := []struct{ src, want string }{
		{"Date.new(2024, 1, 1)!", "(Date.new(2024, 1, 1))!"},
		{`Map.get(m, "k")!`, `(Map.get(m, "k"))!`},
		{"user.nickname!", "(user.nickname)!"},
		{"found!", "(found)!"},
		{"user.address!.city", "(user.address)!.city"},
		{"f(x)!.y", "(f(x))!.y"},
		{"(a |> b())!", "([(a |> b())])!"},
		{`Row.get<Int>(row, "n")!`, `(Row.get(row, "n"))!`},
		{"not x!", "not(x)!"},
		{"x!!", "((x)!)!"},
		{"is_valid!", "(is_valid)!"},
		{"x! == y", "((x)! == y)"},
		{"f(x)! + 1", "((f(x))! + 1)"},
	}
	for _, tc := range cases {
		if got := shape(parseExpr(t, tc.src)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A `!` parses after every literal, so the formatter can drop the
// parentheses of `(0d)!` without changing what the source means. The
// checker refuses the result, since no literal is a Result or a Maybe.
func TestUnwrap_FollowsEveryLiteral(t *testing.T) {
	for _, src := range []string{
		"1!", "1.5!", "0d!", "'a'!", `"x"!`, `"x${y}"!`, `"""x"""!`,
		"`raw`!", `Sql"x"!`, "Regex`x`!", "&1!",
	} {
		if _, ok := parseExpr(t, src).(*ast.Unwrap); !ok {
			t.Errorf("%s: no Unwrap at the top", src)
		}
	}
}

// As a pipe stage, the `!` follows the stage's call and the pipe holds it
// as its right operand.
func TestUnwrap_PipeStage(t *testing.T) {
	got := shape(parseExpr(t, `"4" |> String.to_int()! |> Int.abs()`))
	if want := `(("4" |> (String.to_int())!) |> Int.abs())`; got != want {
		t.Errorf("%s, want %s", got, want)
	}
}

// `!=` is still not-equal, written with or without spaces, and a prefix `!`
// is an error that names `not`.
func TestUnwrap_NotEqualAndNegationKeepTheirMeaning(t *testing.T) {
	toks := lexer.Lex("x!= y")
	if len(toks) < 3 || toks[1].Type != token.BANGEQ {
		t.Fatalf("x!= y lexes as %v", toks)
	}
	for _, src := range []string{"a != b", "a!=b", "x!= y"} {
		if b, ok := parseExpr(t, src).(*ast.Binary); !ok || b.Op != "!=" {
			t.Errorf("%s: %s", src, shape(parseExpr(t, src)))
		}
	}
	if _, err := Parse(lexer.Lex("!f(x)")); err == nil || !strings.Contains(err.Error(), "Nomi spells logical not `not`") {
		t.Errorf("!f(x): %v", err)
	}
	toks = lexer.Lex("is_valid!")
	if len(toks) < 2 || toks[0].Type != token.IDENT || toks[0].Lexeme != "is_valid" || toks[1].Type != token.BANG {
		t.Errorf("is_valid! lexes as %v", toks)
	}
}

// A `not` at the start of a line is a negation that starts a statement; the
// `!` at the end of the line before belongs to its own line's expression.
func TestUnwrap_DoesNotJoinLines(t *testing.T) {
	nodes := parse(t, "a = f(x)!\nnot done\n(g)\n")
	if len(nodes) != 3 {
		t.Fatalf("%d statements", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok || shape(b.Value) != "(f(x))!" {
		t.Errorf("first statement: %#v", nodes[0])
	}
	if es, ok := nodes[1].(*ast.ExprStmt); !ok || shape(es.Expr) != "notdone" {
		t.Errorf("second statement: %#v", nodes[1])
	}
}

// `&Vector.get(v, &1)!` is a capture whose body unwraps the call, not a
// partial application, so its `&1` stays the capture's parameter.
func TestUnwrap_CaptureBody(t *testing.T) {
	c, ok := parseExpr(t, "&Vector.get(v, &1)!").(*ast.Capture)
	if !ok {
		t.Fatalf("not a capture")
	}
	u, ok := c.Body.(*ast.Unwrap)
	if !ok || c.PartialCall() != nil {
		t.Fatalf("body: %#v", c.Body)
	}
	if _, param := u.Expr.(*ast.Call).Args[1].(*ast.PlaceholderParam); !param {
		t.Errorf("`&1` is not the capture's parameter")
	}
}
