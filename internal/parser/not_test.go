package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// notShape renders n with every operation parenthesized, so a test reads
// what `not` takes: `(not (a == b))`.
func notShape(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Unary:
		return "(" + v.Op + " " + notShape(v.Right) + ")"
	case *ast.Binary:
		return "(" + notShape(v.Left) + " " + v.Op + " " + notShape(v.Right) + ")"
	case *ast.GroupedExpr:
		return "[" + notShape(v.Expr) + "]"
	case *ast.TryOp:
		if v.Expr == nil {
			return "?"
		}
		return notShape(v.Expr) + "?"
	case *ast.Unwrap:
		return notShape(v.Expr) + "!"
	case *ast.Call:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = notShape(a)
		}
		return notShape(v.Func) + "(" + strings.Join(args, ", ") + ")"
	case *ast.FieldAccess:
		return notShape(v.Object) + "." + v.Field.Name
	case *ast.Ident:
		return v.Name
	case *ast.TypeIdent:
		return v.Name
	case *ast.IntLit:
		return fmt.Sprint(v.Value)
	}
	return fmt.Sprintf("%T", n)
}

// `not` binds as Python's does: looser than every comparison, arithmetic,
// range and pipe operator, tighter than `and` and `or`.
func TestNot_Precedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"not x", "(not x)"},
		{"not a == b", "(not (a == b))"},
		{"not a != b", "(not (a != b))"},
		{"not a < b", "(not (a < b))"},
		{"not a < b and c", "((not (a < b)) and c)"},
		{"not a and b", "((not a) and b)"},
		{"not a or b and c", "((not a) or (b and c))"},
		{"a and not b", "(a and (not b))"},
		{"a or not b and c", "(a or ((not b) and c))"},
		{"not a + 1 == b * 2", "(not ((a + 1) == (b * 2)))"},
		{"not x |> f()", "(not (x |> f()))"},
		{"not x |> f() and y", "((not (x |> f())) and y)"},
		{"not x?", "(not x?)"},
		{"not x!", "(not x!)"},
		{"not f(x)?", "(not f(x)?)"},
		{"not not x", "(not (not x))"},
		{"not (a and b)", "(not [(a and b)])"},
		{"(not a) == b", "([(not a)] == b)"},
		{"not -x", "(not (- x))"},
		{"not a.b", "(not a.b)"},
	} {
		if got := notShape(parseExpr(t, tc.src)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A pipe stage can start with `not`, which negates the stage's result:
// `x |> not f()` is `not f(x)`. The pipe holds the negation as its stage, so
// the stages after it apply to the negated value.
func TestNot_PipeStage(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x |> not f()", "(x |> (not f()))"},
		{"x |> not Set.contains(seen)", "(x |> (not Set.contains(seen)))"},
		{"x |> not f() |> g()", "((x |> (not f())) |> g())"},
		{"x |> not f()!", "(x |> (not f()!))"},
		{"x |> not not f()", "(x |> (not (not f())))"},
		{"x |> f() |> not g() and y", "(((x |> f()) |> (not g())) and y)"},
	} {
		if got := notShape(parseExpr(t, tc.src)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.src, got, tc.want)
		}
	}
}

// The C-family `!x` is an error that names `not`, as `&&` names `and`; a
// `not` where only a tighter operand may stand asks for parentheses; and a
// `not` stage cannot end in `?`.
func TestNot_Errors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x = !y\n", "Nomi spells logical not `not`: write `not x`, not `!x`"},
		{"x = !f(y) and z\n", "Nomi spells logical not `not`"},
		{"x = &!f(&1)\n", "Nomi spells logical not `not`"},
		{"x = a == not b\n", "`not` binds looser than comparisons and arithmetic, so it cannot be their operand"},
		{"x = a + not b\n", "`not` binds looser than comparisons and arithmetic"},
		{"x = -not b\n", "`not` binds looser than comparisons and arithmetic"},
		{"x = &not f(&1)\n", "`not` binds looser than comparisons and arithmetic"},
		{"x = y |> not f()?\n", "a `not` stage cannot end in `?`"},
	} {
		_, err := Parse(lexer.Lex(tc.src))
		if err == nil {
			t.Errorf("%q parses; want the error %q", tc.src, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %q, want it to contain %q", tc.src, err, tc.want)
		}
	}
}

// `not` is a keyword, so it names nothing.
func TestNot_IsAKeyword(t *testing.T) {
	if _, err := Parse(lexer.Lex("not = 1\n")); err == nil {
		t.Errorf("`not = 1` parses")
	}
}
