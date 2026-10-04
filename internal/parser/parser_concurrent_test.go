package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// TestParseConcurrentBlock_LiteralBody — `concurrent { 42 }` produces a
// ConcurrentBlock node with a Block body containing the integer literal.
// Asserts position info, body wiring, and node type — not just absence
// of error.
func TestParseConcurrentBlock_LiteralBody(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    42
  }
}`
	nodes := parse(t, src)
	fn := nodes[0].(*ast.FuncDef)
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in main, got %d", len(fn.Body.Stmts))
	}
	exprStmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt wrapping ConcurrentBlock, got %T", fn.Body.Stmts[0])
	}
	cb, ok := exprStmt.Expr.(*ast.ConcurrentBlock)
	if !ok {
		t.Fatalf("expected *ast.ConcurrentBlock, got %T", exprStmt.Expr)
	}
	if cb.Line == 0 || cb.Col == 0 {
		t.Errorf("expected Line/Col on concurrent-block, got line=%d col=%d", cb.Line, cb.Col)
	}
	if cb.Body == nil {
		t.Fatalf("expected Body, got nil")
	}
	if len(cb.Body.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in concurrent body, got %d", len(cb.Body.Stmts))
	}
	intStmt, ok := cb.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt wrapping IntLit in body, got %T", cb.Body.Stmts[0])
	}
	if _, ok := intStmt.Expr.(*ast.IntLit); !ok {
		t.Errorf("expected IntLit, got %T", intStmt.Expr)
	}
}

// TestParseConcurrentBlock_SpawnAwait — the canonical layer-1 smoke
// shape: a binding wrapping `Task.spawn(|| ...)`, then `Task.await(x)` as the
// tail expression. Asserts body has two statements (binding + call) so
// the parser produces a well-formed AST the analyzer and checker can
// walk.
func TestParseConcurrentBlock_SpawnAwait(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    x = Task.spawn(|| 42)
    Task.await(x)
  }
}`
	nodes := parse(t, src)
	fn := nodes[0].(*ast.FuncDef)
	exprStmt := fn.Body.Stmts[0].(*ast.ExprStmt)
	cb, ok := exprStmt.Expr.(*ast.ConcurrentBlock)
	if !ok {
		t.Fatalf("expected ConcurrentBlock, got %T", exprStmt.Expr)
	}
	if cb.Body == nil {
		t.Fatalf("expected Body, got nil")
	}
	if got := len(cb.Body.Stmts); got != 2 {
		t.Fatalf("expected 2 stmts in concurrent body, got %d", got)
	}
	if _, ok := cb.Body.Stmts[0].(*ast.Binding); !ok {
		t.Errorf("expected first stmt to be *ast.Binding (x = Task.spawn(...)), got %T", cb.Body.Stmts[0])
	}
	tailStmt, ok := cb.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected second stmt to be ExprStmt (Task.await(x)), got %T", cb.Body.Stmts[1])
	}
	if _, ok := tailStmt.Expr.(*ast.Call); !ok {
		t.Errorf("expected tail expr to be Call (Task.await(x)), got %T", tailStmt.Expr)
	}
}
