package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// parseForTailTest parses src and returns the AST nodes plus a flat list of
// Call nodes in source order (depth-first), so tests can assert IsTailCall on
// each by index. Callers are expected to invoke MarkTailCalls(nodes) before
// reading IsTailCall on the returned slice.
func parseForTailTest(t *testing.T, src string) ([]ast.Node, []*ast.Call) {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Lower derive declarations into top-level *ast.ImplBlock nodes, the form
	// MarkTailCalls visits. Top-level impl blocks are already *ast.ImplBlock
	// values from the parser and pass through unchanged.
	nodes, _ = LowerDerives(nodes)
	var calls []*ast.Call
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		if c, ok := n.(*ast.Call); ok {
			calls = append(calls, c)
		}
		// Minimal walker — recurse via reflection-free shallow inspection.
		switch x := n.(type) {
		case *ast.FuncDef:
			for _, p := range x.Params {
				walk(p.Default)
			}
			if x.Body != nil {
				for _, s := range x.Body.Stmts {
					walk(s)
				}
			}
		case *ast.Lambda:
			for _, p := range x.Params {
				walk(p.Default)
			}
			if x.Body != nil {
				for _, s := range x.Body.Stmts {
					walk(s)
				}
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Call:
			walk(x.Func)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.NamedArg:
			walk(x.Value)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.Case:
			walk(x.Value)
			for _, br := range x.Branches {
				walk(br.Guard)
				walk(br.Body)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Return:
			walk(x.Value)
		case *ast.TryOp:
			walk(x.Expr)
		case *ast.With:
			walk(x.Value)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binding:
			walk(x.Value)
		case *ast.ImplBlock:
			for _, item := range x.Items {
				walk(item)
			}
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return nodes, calls
}

func TestMarkTailCalls_FunctionBodyLastExpression(t *testing.T) {
	src := `fn f(n: Int): Int { f(n) }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !calls[0].IsTailCall {
		t.Errorf("recursive call in fn body's last position should be marked IsTailCall=true")
	}
}

// TestMarkTailCalls_ImplBlockMethodBody: a method inside an `impl` block is a
// tail context just like a top-level fn — the recursive call in its body's
// tail position must be marked. Regression for TCO through block-form
// interface dispatch (MarkTailCalls used to visit only top-level FuncDefs, so
// deep tail recursion inside an impl-block method overflowed the host stack).
func TestMarkTailCalls_ImplBlockMethodBody(t *testing.T) {
	src := `interface Stepper {
  fn step(value: self, n: Int): Int
}

struct Down {}

impl Stepper for Down {
  fn step(value: Down, n: Int): Int {
    if n == 0 {
      0
    } else {
      Stepper.step(value, n - 1)
    }
  }
}

`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)

	if len(calls) != 1 {
		t.Fatalf("expected 1 call (Stepper.step), got %d", len(calls))
	}
	if !calls[0].IsTailCall {
		t.Errorf("recursive call in impl-block method body's tail position should be marked IsTailCall=true")
	}
}

func TestMarkTailCalls_BindingRHSNotTail(t *testing.T) {
	// binding always returns Unit; the call on RHS doesn't propagate to
	// the function's return.
	src := `fn f(): Int {
  x = f()
  0
}
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if calls[0].IsTailCall {
		t.Errorf("call on RHS of a binding is NOT in tail position (binding evaluates to Unit)")
	}
}

func TestMarkTailCalls_ExprStmtPropagates(t *testing.T) {
	// ExprStmt is a pass-through wrapper around an expression.
	src := `fn f(): Int { f() }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if !calls[0].IsTailCall {
		t.Errorf("call wrapped in ExprStmt at fn body end IS tail")
	}
}

func TestMarkTailCalls_IfElseArms(t *testing.T) {
	src := `fn f(n: Int): Int {
  if n == 0 { f(n) } else { f(n - 1) }
}
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: f(n) (then-arm), f(n-1) (else-arm). cond() doesn't appear here.
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	for i, c := range calls {
		if !c.IsTailCall {
			t.Errorf("call %d should be tail (if/else arm in fn body's last position)", i)
		}
	}
}

func TestMarkTailCalls_CaseBranches(t *testing.T) {
	src := `fn f(_n: Int): Int {
  case scrutinee() {
    0 -> base()
    _ when guard() == True -> step()
  }
}
fn scrutinee(): Int { 0 }
fn base(): Int { 0 }
fn guard(): Bool { True }
fn step(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// Order of `calls`: scrutinee, base, guard, step.
	checkByOrder := func(want []bool) {
		if len(calls) != len(want) {
			t.Fatalf("expected %d calls, got %d", len(want), len(calls))
		}
		for i, w := range want {
			if calls[i].IsTailCall != w {
				t.Errorf("call %d (%s): want IsTailCall=%v, got %v",
					i, callsiteString(calls[i]), w, calls[i].IsTailCall)
			}
		}
	}
	// scrutinee NOT tail; base IS; guard NOT; step IS.
	checkByOrder([]bool{false, true, false, true})
}

// callsiteString is a small helper for clearer error messages.
func callsiteString(c *ast.Call) string {
	switch fn := c.Func.(type) {
	case *ast.Ident:
		return fn.Name
	}
	return "?"
}

func TestMarkTailCalls_PipeRHS(t *testing.T) {
	src := `fn f(): Int {
  source() |> sink()
}
fn source(): Int { 0 }
fn sink(x: Int): Int { x }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: source (LHS), sink (RHS). sink IS tail; source NOT.
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].IsTailCall {
		t.Errorf("LHS of |> not in tail position")
	}
	if !calls[1].IsTailCall {
		t.Errorf("RHS of |> IS tail when the |> is in tail position")
	}
}

func TestMarkTailCalls_ShortCircuitRHS(t *testing.T) {
	// Nomi spells short-circuit ops as `and` / `or`, not `&&` / `||`.
	src := `fn f(): Bool {
  pred() and ready()
}
fn pred(): Bool { True }
fn ready(): Bool { True }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if calls[0].IsTailCall {
		t.Errorf("LHS of `and` not tail")
	}
	if !calls[1].IsTailCall {
		t.Errorf("RHS of `and` IS tail (short-circuit second operand becomes the value)")
	}
}

func TestMarkTailCalls_ReturnValue(t *testing.T) {
	src := `fn f(): Int {
  return f()
}
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if !calls[0].IsTailCall {
		t.Errorf("Return.Value's call IS tail")
	}
}

func TestMarkTailCalls_With(t *testing.T) {
	src := `fn f(): Int {
  with App.logger = make_logger()
  log_then_return()
}
fn make_logger(): Int { 0 }
fn log_then_return(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: make_logger (the installed value — NOT tail), log_then_return
	// (the body's last statement, which the override lasts through — tail).
	if calls[0].IsTailCall {
		t.Errorf("the installed value is NOT tail")
	}
	if !calls[1].IsTailCall {
		t.Errorf("block's last stmt IS tail")
	}
}

func TestMarkTailCalls_LambdaBodyMarksOwnTailCall(t *testing.T) {
	// A lambda's last expression is in tail position relative to the LAMBDA,
	// regardless of where the lambda itself sits. This test confirms calls
	// in a lambda's last-stmt position get marked.
	src := `fn invoke(cb: () -> Int): Int { cb() }
fn f(): Int {
  invoke(|| f())
}
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: invoke(...) — fn body's last expr, IS tail.
	//        cb() — inside invoke's body, IS tail (its body's last stmt).
	//        f() — inside lambda body, IS tail relative to the lambda.
	if len(calls) < 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	// Find the f() call inside the lambda by name.
	var lambdaInner *ast.Call
	for _, c := range calls {
		if id, ok := c.Func.(*ast.Ident); ok && id.Name == "f" {
			lambdaInner = c
			break
		}
	}
	if lambdaInner == nil {
		t.Fatalf("did not find f() call inside lambda")
	}
	if !lambdaInner.IsTailCall {
		t.Errorf("call in lambda body's last position IS tail (relative to the lambda)")
	}
}

func TestMarkTailCalls_LambdaInsideNonTailDoesntInherit(t *testing.T) {
	// Outer non-tail context (binding RHS) must not propagate INTO the lambda;
	// the lambda body is walked with tail=true at its boundary.
	src := `fn invoke(cb: () -> Int): Int { cb() }
fn f(): Int {
  result = invoke(|| f())
  result + 1
}
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls: invoke(...) — binding RHS, NOT tail.
	//        cb() — inside invoke's body, IS tail.
	//        f() — inside lambda body, IS tail relative to the lambda
	//              (regardless of outer non-tail context).
	var invokeCall, lambdaInner *ast.Call
	for _, c := range calls {
		if id, ok := c.Func.(*ast.Ident); ok {
			switch id.Name {
			case "invoke":
				invokeCall = c
			case "f":
				lambdaInner = c
			}
		}
	}
	if invokeCall == nil || lambdaInner == nil {
		t.Fatalf("expected to find invoke() and f() calls, got %d calls", len(calls))
	}
	if invokeCall.IsTailCall {
		t.Errorf("invoke(...) as binding RHS not tail")
	}
	if !lambdaInner.IsTailCall {
		t.Errorf("f() IS tail relative to the lambda body, even when the lambda is in a non-tail outer context")
	}
}

func TestMarkTailCalls_DefaultValueNotTail(t *testing.T) {
	src := `fn f(n: Int = compute()): Int { n }
fn compute(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (compute()), got %d", len(calls))
	}
	if calls[0].IsTailCall {
		t.Errorf("default-value expression is NOT tail")
	}
}

func TestMarkTailCalls_TryOpExprNotTail(t *testing.T) {
	// try f() unwraps Result; the call's value is pattern-matched between the
	// call's return and the function's return. The sub-expr is NOT tail.
	src := `fn f(): Int {
  try recurse()
}
fn recurse(): Result<Int, String> { Ok(0) }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if calls[0].IsTailCall {
		t.Errorf("call inside try expr is NOT in tail position (try does pattern-matching)")
	}
}

func TestMarkTailCalls_ImplMethodBody(t *testing.T) {
	src := `interface Greet {
  fn say(value: self): String
}

struct Hi {}

impl Greet for Hi {
  fn say(_value: Hi): String {
    helper()
  }
}

fn helper(): String {
  "hi"
}

`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (helper()), got %d", len(calls))
	}
	if !calls[0].IsTailCall {
		t.Errorf("call in last position of impl-method body IS tail")
	}
}

func TestMarkTailCalls_AggregateLiteralArgsNotTail(t *testing.T) {
	src := `fn f(): List<Int> {
  [g(), g()]
}
fn g(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	for i, c := range calls {
		if c.IsTailCall {
			t.Errorf("call %d inside list literal is NOT tail (literal value is the list, not any element)", i)
		}
	}
}

func TestMarkTailCalls_NamedArgValueWalked(t *testing.T) {
	src := `fn f(_x: Int): Int { f(x: g()) }
fn g(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	// calls[0] = f(x: g())  (outer, IS tail), calls[1] = g() (inside NamedArg.Value, NOT tail)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (f and g), got %d — parseForTailTest may not be walking NamedArg.Value", len(calls))
	}
	if !calls[0].IsTailCall {
		t.Errorf("outer f(...) should be tail (last stmt of fn body)")
	}
	if calls[1].IsTailCall {
		t.Errorf("g() inside NamedArg.Value is NOT tail (call args are never tail)")
	}
}

func TestMarkTailCalls_BlockPrecedingStatementsNotTail(t *testing.T) {
	src := `fn f(): Int {
  side_effect()
  f()
}
fn side_effect(): Int { 0 }
`
	nodes, calls := parseForTailTest(t, src)
	MarkTailCalls(nodes)
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(calls))
	}
	if calls[0].IsTailCall {
		t.Errorf("side_effect() (preceding stmt) should NOT be tail")
	}
	if !calls[1].IsTailCall {
		t.Errorf("f() (last stmt) should be tail")
	}
}

// collectCalls walks the given AST node tree and returns every *ast.Call it
// finds, in source order. Used by the pipeline integration test below to
// inspect IsTailCall marks on AST nodes that were processed via the
// production document.go / internal/frontend analysis pipelines.
func collectCalls(root ast.Node) []*ast.Call {
	var calls []*ast.Call
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		if c, ok := n.(*ast.Call); ok {
			calls = append(calls, c)
		}
		switch x := n.(type) {
		case *ast.FuncDef:
			for _, p := range x.Params {
				walk(p.Default)
			}
			if x.Body != nil {
				for _, s := range x.Body.Stmts {
					walk(s)
				}
			}
		case *ast.Lambda:
			if x.Body != nil {
				for _, s := range x.Body.Stmts {
					walk(s)
				}
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Call:
			walk(x.Func)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.NamedArg:
			walk(x.Value)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binding:
			walk(x.Value)
		}
	}
	walk(root)
	return calls
}

// TestMarkTailCalls_RunsViaLSPDocumentPipeline verifies that the LSP /
// document analysis pipeline (DocumentManager.analyze, invoked via
// DocumentManager.Open) calls MarkTailCalls so AST nodes carry correct
// IsTailCall marks downstream. If anyone removes the wiring in
// document.go's analyze method, this test fires.
//
// This test does NOT call MarkTailCalls itself — it relies entirely on
// the production-path entry point (Open → analyze) to set the mark, and
// reads the result off doc.Nodes.
func TestMarkTailCalls_RunsViaLSPDocumentPipeline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.nomi")
	src := "fn f(n: Int): Int { f(n) }\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	dm := NewDocumentManager()
	doc := dm.Open("file://"+path, src)
	if doc == nil {
		t.Fatal("Open returned nil document")
	}
	if len(doc.Nodes) == 0 {
		t.Fatal("doc has no parsed nodes")
	}

	var calls []*ast.Call
	for _, n := range doc.Nodes {
		calls = append(calls, collectCalls(n)...)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (f(n) recursive), got %d", len(calls))
	}
	if !calls[0].IsTailCall {
		t.Fatalf("expected calls[0].IsTailCall=true after Open→analyze; got false. " +
			"This means MarkTailCalls is no longer wired into DocumentManager.analyze.")
	}
}
