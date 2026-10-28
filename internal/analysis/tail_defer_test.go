package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A call in tail position while a `defer` is pending is not a tail call:
// the deferred call runs when its block ends, after the call returns.
func TestMarkTailCalls_PendingDeferKeepsTheFrame(t *testing.T) {
	src := `fn f(n: Int) -> Int {
  defer g(0)
  if n == 0 {
    return h()
  }
  f(n - 1)
}
fn g(n: Int) -> Int { n }
fn h() -> Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: h (a return value) and f (the last statement), then the calls
	// in the bodies of g and h. The defer call itself is not walked.
	if calls[0].IsTailCall || calls[1].IsTailCall {
		t.Errorf("a call with a defer pending is marked tail: return h() %v, f(n - 1) %v", calls[0].IsTailCall, calls[1].IsTailCall)
	}
}

// Only a defer in a block around the call is pending: one in a block that
// has ended, and one in the function around a lambda, leave the call a
// tail call.
func TestMarkTailCalls_EndedOrOuterDeferIsNotPending(t *testing.T) {
	src := `fn f(n: Int) -> Int {
  if n == 0 {
    defer g(0)
    g(1)
  } else {
    g(2)
  }
  f(n - 1)
}
fn k(n: Int) -> (Int) -> Int {
  defer g(0)
  fn x -> f(x)
}
fn g(n: Int) -> Int { n }
`
	nodes, _ := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	tail := map[int]bool{}
	ast.Inspect(&ast.Block{Stmts: nodes}, func(n ast.Node) bool {
		if c, ok := n.(*ast.Call); ok {
			if id, ok := c.Func.(*ast.Ident); ok && id.Name == "f" {
				tail[c.Line] = c.IsTailCall
			}
		}
		return true
	})
	if !tail[8] {
		t.Errorf("f(n - 1) after an if whose branch deferred is not marked tail")
	}
	if !tail[12] {
		t.Errorf("a lambda body call is not marked tail because the function around it deferred")
	}
}
