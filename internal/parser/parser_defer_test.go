package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

func TestParseDefer(t *testing.T) {
	src := `fn main() {
  db = sqlite.temp()
  defer sqlite.close(db)
  run(db)
}`
	nodes := parse(t, src)
	fn := nodes[0].(*ast.FuncDef)
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 stmts in main, got %d", len(fn.Body.Stmts))
	}
	deferStmt, ok := fn.Body.Stmts[1].(*ast.Defer)
	if !ok {
		t.Fatalf("expected Defer, got %T", fn.Body.Stmts[1])
	}
	if deferStmt.Line == 0 || deferStmt.Col == 0 {
		t.Errorf("expected Line/Col on defer, got line=%d col=%d", deferStmt.Line, deferStmt.Col)
	}
	call, ok := deferStmt.Call.(*ast.Call)
	if !ok {
		t.Fatalf("expected Call, got %T", deferStmt.Call)
	}
	if len(call.Args) != 1 {
		t.Errorf("expected 1 deferred call arg, got %d", len(call.Args))
	}
}

func TestParseDefer_AllowsNamedArgs(t *testing.T) {
	src := `fn main() {
  defer Timer.stop(timer: t)
}`
	nodes := parse(t, src)
	fn := nodes[0].(*ast.FuncDef)
	deferStmt := fn.Body.Stmts[0].(*ast.Defer)
	call, ok := deferStmt.Call.(*ast.Call)
	if !ok {
		t.Fatalf("expected Call, got %T", deferStmt.Call)
	}
	if _, ok := call.Args[0].(*ast.NamedArg); !ok {
		t.Fatalf("expected NamedArg, got %T", call.Args[0])
	}
}
