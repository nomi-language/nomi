package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `?` binds as tightly as a call, a field access and a postfix `!`.
func TestQuestion_BindsAsAPostfix(t *testing.T) {
	// `load()?.name` reads the field of the payload.
	fa, ok := parseExpr(t, "load()?.name").(*ast.FieldAccess)
	if !ok {
		t.Fatalf("load()?.name is not a field access")
	}
	if try, ok := fa.Object.(*ast.TryOp); !ok || try.Expr == nil {
		t.Fatalf("the field's object is %T, want a `?`", fa.Object)
	}
	// `user.address?.city`.
	fa = parseExpr(t, "user.address?.city").(*ast.FieldAccess)
	if try, ok := fa.Object.(*ast.TryOp); !ok {
		t.Fatalf("object is %T", fa.Object)
	} else if inner, ok := try.Expr.(*ast.FieldAccess); !ok || inner.Field.Name != "address" {
		t.Fatalf("`?` operand is %#v", try.Expr)
	}
	// `a.b()?.c()?`: each `?` takes the call before it.
	outer, ok := parseExpr(t, "a.b()?.c()?").(*ast.TryOp)
	if !ok {
		t.Fatalf("a.b()?.c()? is not a `?`")
	}
	call, ok := outer.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("operand is %T", outer.Expr)
	}
	callee := call.Func.(*ast.FieldAccess)
	if _, ok := callee.Object.(*ast.TryOp); !ok {
		t.Fatalf("callee's object is %T, want a `?`", callee.Object)
	}
	// `f()!?` and `f()?!`.
	if try, ok := parseExpr(t, "f()!?").(*ast.TryOp); !ok {
		t.Fatalf("f()!? is not a `?`")
	} else if _, ok := try.Expr.(*ast.Unwrap); !ok {
		t.Fatalf("f()!? operand is %T", try.Expr)
	}
	if u, ok := parseExpr(t, "f()?!").(*ast.Unwrap); !ok {
		t.Fatalf("f()?! is not a `!`")
	} else if _, ok := u.Expr.(*ast.TryOp); !ok {
		t.Fatalf("f()?! operand is %T", u.Expr)
	}
	// A prefix `not` negates the `?`'s value: `not ready()?`.
	un, ok := parseExpr(t, "not ready()?").(*ast.Unary)
	if !ok || un.Op != "not" {
		t.Fatalf("not ready()? is not a negation")
	}
	if _, ok := un.Right.(*ast.TryOp); !ok {
		t.Fatalf("negated %T, want a `?`", un.Right)
	}
	// `a()? + b()?`.
	bin := parseExpr(t, "a()? + b()?").(*ast.Binary)
	if _, ok := bin.Left.(*ast.TryOp); !ok {
		t.Fatalf("left is %T", bin.Left)
	}
	if _, ok := bin.Right.(*ast.TryOp); !ok {
		t.Fatalf("right is %T", bin.Right)
	}
}

// In a pipe, a `?` after a stage is a `?` stage of its own: `text |>
// parse()? |> validate()?` is four stages.
func TestQuestion_PipeStages(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "text |> parse()? |> validate()?"))
	if len(stages) != 4 {
		t.Fatalf("got %d stages, want 4", len(stages))
	}
	for i, want := range []string{"Call", "TryOp", "Call", "TryOp"} {
		if got := stages[i].NodeType(); got != want {
			t.Errorf("stage %d is %s, want %s", i, got, want)
		}
	}
	if try := stages[1].(*ast.TryOp); try.Expr != nil {
		t.Errorf("a `?` stage has no operand")
	}
	// A `?` after a `case` stage.
	_, stages = pipeStages(t, parseExpr(t, "x |> case { _ -> f() }?"))
	if len(stages) != 2 || stages[1].NodeType() != "TryOp" {
		t.Fatalf("stages %v", stages)
	}
}

// `|> then? fn v -> ...` is the `then` stage and then a `?` stage; `tap?`
// is an error.
func TestQuestion_ThenStage(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "x |> then? fn v -> parse(v) |> dbg"))
	if len(stages) != 3 {
		t.Fatalf("got %d stages, want 3", len(stages))
	}
	if _, ok := stages[0].(*ast.Then); !ok {
		t.Fatalf("stage 0 is %T", stages[0])
	}
	if try, ok := stages[1].(*ast.TryOp); !ok || try.Expr != nil {
		t.Fatalf("stage 1 is %T", stages[1])
	}
	err := parseError(t, "x = 5 |> tap? fn v -> io.print(v)")
	if !strings.Contains(err.Error(), "`tap` passes its input on unchanged") {
		t.Errorf("tap?: %v", err)
	}
}

// A `?` after a name is the operator whatever follows it: `f?(x)`
// propagates `f` and calls its payload.
func TestQuestion_AfterAName(t *testing.T) {
	call, ok := parseExpr(t, "handler?(x)").(*ast.Call)
	if !ok {
		t.Fatalf("handler?(x) is not a call")
	}
	if q, ok := call.Func.(*ast.TryOp); !ok {
		t.Fatalf("callee is %T, want a `?`", call.Func)
	} else if id, ok := q.Expr.(*ast.Ident); !ok || id.Name != "handler" {
		t.Fatalf("`?` operand is %#v", q.Expr)
	}
	call = parseExpr(t, "Iter.is_empty?(xs)").(*ast.Call)
	if _, ok := call.Func.(*ast.TryOp); !ok {
		t.Fatalf("Iter.is_empty?(xs)'s callee is %T, want a `?`", call.Func)
	}
	if _, ok := parseExpr(t, "handler?").(*ast.TryOp); !ok {
		t.Errorf("handler? is not a `?`")
	}
}

// A `?` that starts a line is not written against the line above.
func TestQuestion_OnTheExpressionsLine(t *testing.T) {
	if _, err := Parse(lexer.Lex("fn f() -> Maybe<Int> {\n    x = g()\n    ?\n}")); err == nil {
		t.Errorf("a `?` alone on a line parsed")
	}
}
