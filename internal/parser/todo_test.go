package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func TestTodo_BareAndWithReason(t *testing.T) {
	bare, ok := parseExpr(t, "todo").(*ast.Todo)
	if !ok || bare.Reason != nil || bare.Line != 1 || bare.Col != 1 {
		t.Fatalf("bare todo: %#v", bare)
	}
	for src, want := range map[string]struct {
		value       string
		triple, raw bool
	}{
		`todo "parse the header"`:            {"parse the header", false, false},
		"todo `raw ${not} interpolated`":     {"raw ${not} interpolated", false, true},
		"todo \"\"\"\n    multi\n    \"\"\"": {"multi", true, false},
	} {
		td, ok := parseExpr(t, src).(*ast.Todo)
		if !ok || td.Reason == nil {
			t.Fatalf("%q: %#v", src, td)
		}
		if td.Reason.Value != want.value || td.Reason.Triple != want.triple || td.Reason.Raw != want.raw {
			t.Errorf("%q: reason %#v, want %+v", src, td.Reason, want)
		}
	}
}

// A reason starts on the `todo`'s line: a string on the next line is the next
// statement.
func TestTodo_ReasonOnTheNextLineIsAStatement(t *testing.T) {
	nodes := parse(t, "todo\n\"next\"\n")
	if len(nodes) != 2 {
		t.Fatalf("got %d statements, want 2", len(nodes))
	}
	if td := nodes[0].(*ast.ExprStmt).Expr.(*ast.Todo); td.Reason != nil {
		t.Fatalf("the next line's string became the reason: %#v", td.Reason)
	}
}

func TestTodo_ReasonCannotInterpolateOrCarryATag(t *testing.T) {
	for _, src := range []string{`todo "step ${n}"`, `todo Sql"select 1"`} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "a `todo` reason is a plain string literal: it cannot interpolate or carry a tag") {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestTodo_PipeStageAndArgument(t *testing.T) {
	pipe := parseExpr(t, "3 |> todo").(*ast.Binary)
	if _, ok := pipe.Right.(*ast.Todo); !ok || pipe.Op != "|>" {
		t.Fatalf("pipe stage: %#v", pipe)
	}
	call := parseExpr(t, `f(todo, todo "b")`).(*ast.Call)
	if len(call.Args) != 2 {
		t.Fatalf("args: %#v", call.Args)
	}
	if td, ok := call.Args[1].(*ast.Todo); !ok || td.ReasonText() != "b" {
		t.Fatalf("second argument: %#v", call.Args[1])
	}
}
