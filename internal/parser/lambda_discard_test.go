package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// lambdaDiscard is the one statement of lam's body when it is the discard
// `_ = <call of fn>`, the statement a body below the `->` holds for
// `_ = fn(...)`.
func lambdaDiscard(t *testing.T, lam *ast.Lambda, fn string) *ast.Binding {
	t.Helper()
	if lam.Body == nil || len(lam.Body.Stmts) != 1 {
		t.Fatalf("lambda body %#v, want one statement", lam.Body)
	}
	b, ok := lam.Body.Stmts[0].(*ast.Binding)
	if !ok || b.Name != "_" || b.TypeAnnotation != nil {
		t.Fatalf("lambda body statement %#v, want the discard `_ = ...`", lam.Body.Stmts[0])
	}
	call, ok := b.Value.(*ast.Call)
	if !ok {
		t.Fatalf("discarded value %#v, want a call of %s", b.Value, fn)
	}
	if id, ok := call.Func.(*ast.Ident); !ok || id.Name != fn {
		t.Fatalf("discarded call of %#v, want %s", call.Func, fn)
	}
	return b
}

// A lambda's body on its `->`'s line may be a discard, `_ = expr`, and holds
// the Binding the same body written below the `->` holds.
func TestLambda_OneLineBodyMayBeADiscard(t *testing.T) {
	one := parseExpr(t, "Iter.each(items, fn i -> _ = send(out, i))").(*ast.Call).Args[1].(*ast.Lambda)
	block := parseExpr(t, "Iter.each(items, fn i ->\n    _ = send(out, i)\nend)").(*ast.Call).Args[1].(*ast.Lambda)
	a, b := lambdaDiscard(t, one, "send"), lambdaDiscard(t, block, "send")
	if len(a.Value.(*ast.Call).Args) != 2 || len(b.Value.(*ast.Call).Args) != 2 {
		t.Errorf("discarded calls %#v and %#v, want two arguments each", a.Value, b.Value)
	}
	if one.Body.EndLine != 0 || block.Body.EndLine == 0 {
		t.Errorf("body ends %d and %d, want the one-line body with none", one.Body.EndLine, block.Body.EndLine)
	}
	if a.Line != 1 || a.Col != 26 {
		t.Errorf("discard at %d:%d, want 1:26", a.Line, a.Col)
	}

	// The discarded value runs to the end of the expression, as an
	// expression body does: the pipe is inside it.
	piped := parseExpr(t, "fn i -> _ = check(i) |> describe()").(*ast.Lambda)
	pb, ok := piped.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("piped body %#v, want a discard", piped.Body.Stmts[0])
	}
	if bin, ok := pb.Value.(*ast.Binary); !ok || bin.Op != "|>" {
		t.Errorf("discarded value %#v, want the pipe", pb.Value)
	}

	// A `then` stage's discard ends at the next `|>`, as its expression
	// body does.
	pipe := parseExpr(t, "5 |> then fn v -> _ = check(v) |> then fn u -> u").(*ast.Binary)
	inner, ok := pipe.Left.(*ast.Binary)
	if !ok || inner.Op != "|>" {
		t.Fatalf("pipeline %#v, want two stages", pipe)
	}
	then, ok := inner.Right.(*ast.Then)
	if !ok {
		t.Fatalf("first stage %#v, want a then stage", inner.Right)
	}
	lambdaDiscard(t, then.Lambda, "check")

	// An `else` after the discarded value is the enclosing `if`'s.
	cond := parseExpr(t, "if loud then fn i -> _ = shout(i) else fn i -> _ = say(i)").(*ast.If)
	lambdaDiscard(t, cond.Then.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Lambda), "shout")
	els, ok := cond.Else.(*ast.Block)
	if !ok {
		t.Fatalf("if %#v, want its else", cond)
	}
	lambdaDiscard(t, els.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Lambda), "say")
}

// The discard is the one statement a lambda's body may be on its `->`'s
// line: a binding of a name, an annotated or labeled discard, a
// destructure, `return` and `break` still need the body below the `->`.
func TestLambda_OneLineBodyTakesNoOtherStatement(t *testing.T) {
	for src, want := range map[string]string{
		"Iter.each(items, fn i -> x = send(out, i))":        "line 1, col 28: expected ',' or ')' in argument list",
		"Iter.each(items, fn i -> _unused = send(out, i))":  "line 1, col 34: expected ',' or ')' in argument list",
		"Iter.each(items, fn i -> _: Int = send(out, i))":   "line 1, col 27: expected ',' or ')' in argument list",
		"Iter.each(items, fn i -> (a, b) = send(out, i))":   "line 1, col 33: expected ',' or ')' in argument list",
		"Iter.each(items, fn i -> return send(out, i))":     "line 1, col 26: unexpected `return`",
		"Iter.each(items, fn i -> break)":                   "line 1, col 26: unexpected `break`",
		"Iter.each(items, fn i -> _ = send(out, i) else 0)": "line 1, col 43: expected ',' or ')' in argument list",
		"Iter.each(items, fn i -> _ = x = send(out, i))":    "line 1, col 32: expected ',' or ')' in argument list",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q): %v, want %q", src, err, want)
		}
	}
}
