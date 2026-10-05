package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// pipeStages is the source and stages of the pipeline n, left to right.
func pipeStages(t *testing.T, n ast.Node) (ast.Node, []ast.Node) {
	t.Helper()
	var stages []ast.Node
	for {
		b, ok := n.(*ast.Binary)
		if !ok || b.Op != "|>" {
			return n, stages
		}
		stages = append([]ast.Node{b.Right}, stages...)
		n = b.Left
	}
}

// lambdaBody is the one expression of lam's body.
func lambdaBody(t *testing.T, lam *ast.Lambda) ast.Node {
	t.Helper()
	if lam.Body == nil || len(lam.Body.Stmts) != 1 {
		t.Fatalf("lambda body: %#v", lam.Body)
	}
	return lam.Body.Stmts[0].(*ast.ExprStmt).Expr
}

// A `then` lambda's bare body ends at the next `|>` of its pipeline.
func TestThen_BodyEndsAtTheNextPipe(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "xs |> then |v| v == 1 |> dbg"))
	if len(stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(stages))
	}
	then, ok := stages[0].(*ast.Then)
	if !ok {
		t.Fatalf("stage 1 is %T, want *ast.Then", stages[0])
	}
	if then.Line != 1 || then.Col != 7 || then.Lambda.Col != 12 {
		t.Errorf("then at %d:%d, lambda at col %d", then.Line, then.Col, then.Lambda.Col)
	}
	if b, ok := lambdaBody(t, then.Lambda).(*ast.Binary); !ok || b.Op != "==" {
		t.Errorf("then body is %#v, want `v == 1`", lambdaBody(t, then.Lambda))
	}
	if _, ok := stages[1].(*ast.Dbg); !ok {
		t.Errorf("stage 2 is %T, want *ast.Dbg", stages[1])
	}
}

// Inside a `then` body, a nested lambda and `dbg expr` end at the pipe too.
func TestThen_NestedPrefixesEndAtThePipe(t *testing.T) {
	for _, src := range []string{
		"x |> then |a| |b| a + b |> f()",
		"x |> then |a| dbg a |> f()",
	} {
		_, stages := pipeStages(t, parseExpr(t, src))
		if len(stages) != 2 {
			t.Errorf("%s: got %d stages, want 2", src, len(stages))
		}
	}
}

// Braces keep a pipe inside a `then` lambda.
func TestThen_BracedBodyHoldsAPipe(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "xs |> then |v| { v |> Iter.count() } |> dbg"))
	if len(stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(stages))
	}
	then := stages[0].(*ast.Then)
	if b, ok := lambdaBody(t, then.Lambda).(*ast.Binary); !ok || b.Op != "|>" {
		t.Errorf("then body is %#v, want a pipe", lambdaBody(t, then.Lambda))
	}
}

// Everywhere else a lambda's body runs to the end of its expression: in a
// call's parentheses, and at the head of a pipeline.
func TestLambda_BodyRunsPastAPipe(t *testing.T) {
	call := parseExpr(t, "Iter.map(xs, |s| String.to_int(s) |> Maybe.with_default(0))").(*ast.Call)
	lam := call.Args[1].(*ast.Lambda)
	if b, ok := lambdaBody(t, lam).(*ast.Binary); !ok || b.Op != "|>" {
		t.Errorf("argument lambda body is %#v, want a pipe", lambdaBody(t, lam))
	}
	head := parseExpr(t, "|n| n * 2 |> dbg").(*ast.Lambda)
	if b, ok := lambdaBody(t, head).(*ast.Binary); !ok || b.Op != "|>" {
		t.Errorf("head lambda body is %#v, want a pipe", lambdaBody(t, head))
	}
	// A bare lambda stage is parsed, and its body takes the rest of the
	// pipeline; the checker rejects the stage and names the `then`.
	_, stages := pipeStages(t, parseExpr(t, "x |> |n| n * 2 |> dbg"))
	if len(stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(stages))
	}
	if _, ok := stages[0].(*ast.Lambda); !ok {
		t.Errorf("stage is %T, want *ast.Lambda", stages[0])
	}
}

func TestThen_Errors(t *testing.T) {
	for src, want := range map[string]string{
		"x = then |v| v":            "`then` is a pipe stage: write `value |> then |v| ...`",
		"x = 5 |> then f":           "`then` takes a lambda: write `then |v| ...`",
		"x = 5 |> then":             "`then` takes a lambda: write `then |v| ...`",
		"x = 5 |> try then |v| v":   "`try` does not prefix a `then` stage: write `|> then |v| ...` and `|> try` as two stages",
		"x = 5 |> if then |v| v {}": "`then` is a pipe stage",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %q, want it to contain %q", src, err, want)
		}
	}
}
