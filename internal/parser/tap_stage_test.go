package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A `tap` stage is its own node, and its lambda's bare body ends at the next
// `|>` of its pipeline, as a `then` lambda's does.
func TestTap_BodyEndsAtTheNextPipe(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "xs |> tap |v| io.print(v) |> f()"))
	if len(stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(stages))
	}
	tap, ok := stages[0].(*ast.Tap)
	if !ok {
		t.Fatalf("stage 1 is %T, want *ast.Tap", stages[0])
	}
	if tap.Line != 1 || tap.Col != 7 || tap.Lambda.Col != 11 {
		t.Errorf("tap at %d:%d, lambda at col %d", tap.Line, tap.Col, tap.Lambda.Col)
	}
	if c, ok := lambdaBody(t, tap.Lambda).(*ast.Call); !ok || len(c.Args) != 1 {
		t.Errorf("tap body is %#v, want `io.print(v)`", lambdaBody(t, tap.Lambda))
	}
	if _, ok := stages[1].(*ast.Call); !ok {
		t.Errorf("stage 2 is %T, want *ast.Call", stages[1])
	}
}

// Braces keep a pipe inside a `tap` lambda.
func TestTap_BracedBodyHoldsAPipe(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "xs |> tap |v| { v |> io.print() } |> dbg"))
	if len(stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(stages))
	}
	tap := stages[0].(*ast.Tap)
	if b, ok := lambdaBody(t, tap.Lambda).(*ast.Binary); !ok || b.Op != "|>" {
		t.Errorf("tap body is %#v, want a pipe", lambdaBody(t, tap.Lambda))
	}
}

func TestTap_Errors(t *testing.T) {
	for src, want := range map[string]string{
		"x = tap |v| v":            "`tap` is a pipe stage: write `value |> tap |v| ...`",
		"tap = 1":                  "`tap` is a pipe stage",
		"x = 5 |> tap f":           "`tap` takes a lambda: write `tap |v| ...`",
		"x = 5 |> tap":             "`tap` takes a lambda: write `tap |v| ...`",
		"x = 5 |> try tap |v| v":   "`try` does not prefix a `tap` stage: write `|> tap |v| ...` and `|> try` as two stages",
		"x = 5 |> dbg tap |v| v":   "`dbg` does not prefix a `tap` stage: write `|> tap |v| ...` and `|> dbg` as two stages",
		"x = 5 |> if tap |v| v {}": "`tap` is a pipe stage",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %q, want it to contain %q", src, err, want)
		}
	}
}
