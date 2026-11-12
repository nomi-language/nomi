package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// lambdaOf is the lambda bound by the one statement `f = fn ...`.
func lambdaOf(t *testing.T, src string) *ast.Lambda {
	t.Helper()
	b, ok := parse(t, src)[0].(*ast.Binding)
	if !ok {
		t.Fatalf("Parse(%q) is not a binding", src)
	}
	lam, ok := b.Value.(*ast.Lambda)
	if !ok {
		t.Fatalf("Parse(%q) binds a %T, not a lambda", src, b.Value)
	}
	return lam
}

// A lambda parameter's annotation in parentheses, before the lambda's own
// `->`, is a function type when what follows that type ends a parameter,
// and otherwise the tuple (or grouped) type alone.
func TestLambdaParamTupleOrFunctionAnnotation(t *testing.T) {
	for _, tc := range []struct{ src, param, body string }{
		{"f = fn p: (Int, Int) -> p.first", "(Int, Int)", "FieldAccess"},
		{"f = fn p: (Int, Int) -> p.first", "(Int, Int)", "FieldAccess"},
		{"f = fn p: (Int, Int), q: Int -> q", "(Int, Int)", "Ident"},
		{"f = fn g: (Int) -> Int -> g(1)", "(Int) -> Int", "Call"},
		{"f = fn g: (Int, Int) -> Int, x -> g(x, x)", "(Int, Int) -> Int", "Call"},
		{"f = fn g: ((Int, Int)) -> Int -> g((1, 2))", "((Int, Int)) -> Int", "Call"},
		{"f = fn p: (Int) -> p", "Int", "Ident"},
		{"f = fn\n    g: (Int) -> Int\n-> g(1)", "(Int) -> Int", "Call"},
	} {
		lam := lambdaOf(t, tc.src)
		if len(lam.Params) == 0 || lam.Params[0].TypeAnnotation == nil {
			t.Errorf("Parse(%q): no annotated parameter", tc.src)
			continue
		}
		if got := lam.Params[0].TypeAnnotation.TypeString(); got != tc.param {
			t.Errorf("Parse(%q): parameter type %q, want %q", tc.src, got, tc.param)
		}
		if len(lam.Body.Stmts) != 1 {
			t.Errorf("Parse(%q): body has %d statements", tc.src, len(lam.Body.Stmts))
			continue
		}
		body := lam.Body.Stmts[0]
		if es, ok := body.(*ast.ExprStmt); ok {
			body = es.Expr
		}
		if got := body.NodeType(); got != tc.body {
			t.Errorf("Parse(%q): body is a %s, want a %s", tc.src, got, tc.body)
		}
	}
}

// `fn (a, b) -> ...` takes one tuple apart, at the top level too.
func TestLambdaTupleParameterAtTheTopLevel(t *testing.T) {
	lam := parseExpr(t, "fn (a, b) -> a").(*ast.Lambda)
	if len(lam.Params) != 1 {
		t.Fatalf("params = %d, want 1", len(lam.Params))
	}
	if _, ok := lam.Params[0].Destructure.(*ast.TuplePattern); !ok {
		t.Errorf("param = %#v, want a tuple pattern", lam.Params[0])
	}
}

// A call's `(` is on the line its callee ends on: a `(` that starts the next
// line starts a statement, here a tuple destructure after a `!`.
func TestParenOnTheNextLineIsNotACall(t *testing.T) {
	nodes := parse(t, "fn main()\n    x = f()!\n    (a, b) = x\nend\n")
	body := nodes[0].(*ast.FuncDef).Body.Stmts
	if len(body) != 2 {
		t.Fatalf("got %d statements, want 2", len(body))
	}
	if _, ok := body[1].(*ast.TupleDestructure); !ok {
		t.Errorf("second statement = %T, want a tuple destructure", body[1])
	}
}
