package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

func TestParseDeferOnError(t *testing.T) {
	src := "fn main()\n  defer on error sqlite.close(db)\n  defer on(x)\n  defer sqlite.close(db)\nend"
	nodes := parse(t, src)
	fn := nodes[0].(*ast.FuncDef)
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 stmts in main, got %d", len(fn.Body.Stmts))
	}
	onErr := fn.Body.Stmts[0].(*ast.Defer)
	if !onErr.OnError || onErr.OnLine != 2 || onErr.OnCol != 9 {
		t.Errorf("defer on error: OnError=%v at %d:%d, want true at 2:9", onErr.OnError, onErr.OnLine, onErr.OnCol)
	}
	if _, ok := onErr.Call.(*ast.Call); !ok {
		t.Errorf("defer on error call = %T, want a call", onErr.Call)
	}
	// `on` is a name everywhere else, including as a deferred callee.
	if fn.Body.Stmts[1].(*ast.Defer).OnError {
		t.Error("`defer on(x)` parsed as the error-only form")
	}
	if fn.Body.Stmts[2].(*ast.Defer).OnError {
		t.Error("a plain defer parsed as the error-only form")
	}
}

func TestParseDeferOnErrorNeedsACall(t *testing.T) {
	_, err := Parse(lexer.Lex("fn main()\n  defer on error\nend\n"))
	if err == nil || !strings.Contains(err.Error(), "expected call after `defer on error`") {
		t.Fatalf("got %v, want the missing call reported", err)
	}
}
