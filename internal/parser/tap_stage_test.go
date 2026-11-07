package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `tap` stage is its own node, and its lambda's bare body ends at the next
// `|>` of its pipeline, as a `then` lambda's does.
func TestTap_BodyEndsAtTheNextPipe(t *testing.T) {
	_, stages := pipeStages(t, parseExpr(t, "xs |> tap fn v -> io.print(v) |> f()"))
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
	_, stages := pipeStages(t, parseExpr(t, "xs |> tap fn v -> { v |> io.print() } |> dbg"))
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
		"x = tap fn v -> v":            "`tap` is a pipe stage: write `value |> tap fn v -> ...`",
		"tap = 1":                      "`tap` is a pipe stage",
		"x = 5 |> tap":                 "`tap` takes a function: write `tap f`, `tap fn v -> ...` or `tap &f(&1)`",
		"x = 5 |> tap 42":              "`tap` takes a function: write `tap f`, `tap fn v -> ...` or `tap &f(&1)`",
		"x = 5 |> tap? f":              "`tap` passes its input on unchanged, so there is no result for a `?` to propagate",
		"x = 5 |> tap? fn v -> v":      "`tap` passes its input on unchanged, so there is no result for a `?` to propagate",
		"x = 5 |> dbg tap fn v -> v":   "`dbg` does not prefix a `tap` stage: write `|> tap fn v -> ...` and `|> dbg` as two stages",
		"x = 5 |> if tap fn v -> v {}": "`tap` is a pipe stage",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %q, want it to contain %q", src, err, want)
		}
	}
}

// `tap f` names the function the stage runs: a plain name, a qualified one,
// or a type's function. The stages after it stay in the pipeline.
func TestTap_FunctionByName(t *testing.T) {
	for src, want := range map[string]string{
		"xs |> tap log_it |> f()":   "log_it",
		"xs |> tap io.print |> f()": "io.print",
		"xs |> tap Log.info |> f()": "Log.info",
		"xs |> tap a.b.c |> f()":    "a.b.c",
	} {
		_, stages := pipeStages(t, parseExpr(t, src))
		if len(stages) != 2 {
			t.Fatalf("%s: got %d stages, want 2", src, len(stages))
		}
		tap, ok := stages[0].(*ast.Tap)
		if !ok {
			t.Fatalf("%s: stage 1 is %T, want *ast.Tap", src, stages[0])
		}
		if tap.Lambda != nil || tap.Capture != nil {
			t.Errorf("%s: tap holds a lambda or a capture", src)
		}
		if got, _ := pipeStageFuncName(tap.Name); got != want || tap.Fn() != tap.Name {
			t.Errorf("%s: name %q, want %q", src, got, want)
		}
		if _, ok := stages[1].(*ast.Call); !ok {
			t.Errorf("%s: stage 2 is %T, want *ast.Call", src, stages[1])
		}
	}
}

// A call after `tap` is no function name; the help names the stage that
// passes the value to it.
func TestTap_CallIsNoName(t *testing.T) {
	var pe ParseError
	err := parseError(t, "x = 5 |> tap io.print(1)")
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is no ParseError", err)
	}
	if want := "`tap` takes a function: write `tap f`, `tap fn v -> ...` or `tap &f(&1)`"; pe.Message != want {
		t.Errorf("message %q, want %q", pe.Message, want)
	}
	if want := "write `tap io.print` to call it with the piped value, or a capture such as `tap &io.print(&1, ...)` to pass more arguments"; len(pe.Hints) != 1 || pe.Hints[0] != want {
		t.Errorf("hints %q, want %q", pe.Hints, want)
	}
}

// A bad `tap` or `then` operand is one error: recovery skips to the end of
// the brackets the statement opened and then the statement, so the `}`
// that closes the function is not reported as unexpected.
func TestTap_BadOperandDoesNotCascade(t *testing.T) {
	src := "fn main() {\n  x = case 5 { n -> n |> tap 1 }\n  y = [1] |> Iter.map(fn v -> { v |> then 2 })\n  io.print(x)\n}\n"
	_, errs := ParseAll(lexer.Lex(src))
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(errs), errs)
	}
	if errs[0].Line != 2 || !strings.Contains(errs[0].Message, "`tap` takes a function") {
		t.Errorf("first error %v", errs[0])
	}
	if errs[1].Line != 3 || !strings.Contains(errs[1].Message, "`then` takes a lambda or a capture") {
		t.Errorf("second error %v", errs[1])
	}
}
