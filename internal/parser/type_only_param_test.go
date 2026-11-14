package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A parameter written as its type alone may be any type a named parameter
// may have: a generic, a module-qualified, a function, a tuple, an
// anonymous struct type, in any position, and it binds nothing.
func TestFuncParam_TypeOnlyForms(t *testing.T) {
	for _, tc := range []struct{ src, typ string }{
		{"fn f(Vector<Int>, i: Int) -> Int\n    i\nend", "Vector<Int>"},
		{"fn f(Map<Int, Int>) -> Int\n    1\nend", "Map<Int, Int>"},
		{"fn f(http.Request) -> Int\n    1\nend", "http.Request"},
		{"fn f(mod.Box<Int>) -> Int\n    1\nend", "mod.Box<Int>"},
		{"fn f(Probe.Reading<Int>) -> Int\n    1\nend", "Probe.Reading<Int>"},
		{"fn f((Int) -> Int) -> Int\n    1\nend", "(Int) -> Int"},
		{"fn f((Int, String)) -> Int\n    1\nend", "(Int, String)"},
		{"fn f({x: Int}) -> Int\n    1\nend", "{x: Int}"},
		{"fn f(\n    Vector<Int>\n) -> Int\n    1\nend", "Vector<Int>"},
	} {
		p := funcDefParam(t, tc.src, 0)
		if !p.TypeOnly || p.Destructure != nil || p.Name != "_" {
			t.Errorf("%q: param = %+v, want a TypeOnly parameter", tc.src, p)
			continue
		}
		if got := p.TypeAnnotation.TypeString(); got != tc.typ {
			t.Errorf("%q: type = %q, want %q", tc.src, got, tc.typ)
		}
	}
	p := funcDefParam(t, "fn f(i: Int, List<Int>) -> Int\n    i\nend", 1)
	if !p.TypeOnly || p.TypeAnnotation.TypeString() != "List<Int>" {
		t.Errorf("second param = %+v, want a TypeOnly List<Int>", p)
	}
}

// The forms a pattern is spelled as stay patterns: a variant named through
// its type or file, a tuple of names, a struct pattern, a payload.
func TestFuncParam_TypeOnlyLeavesPatterns(t *testing.T) {
	for _, src := range []string{
		"fn f(Shape.Circle) -> Int\n    1\nend",
		"fn f(geo.Shape.Circle) -> Int\n    1\nend",
		"fn f((a, b): (Int, Int)) -> Int\n    a\nend",
		"fn f((Int, Int): (Int, Int)) -> Int\n    1\nend",
		"fn f({x:}: P) -> Int\n    x\nend",
		"fn f(e.V(x)) -> Int\n    x\nend",
	} {
		if p := funcDefParam(t, src, 0); p.TypeOnly || p.Destructure == nil {
			t.Errorf("%q: param = %+v, want a pattern", src, p)
		}
	}
	if p := funcDefParam(t, "fn f(Request) -> Int\n    1\nend", 0); !p.TypeOnly {
		t.Errorf("fn f(Request): param = %+v, want TypeOnly", p)
	}
}

// A lambda takes the same forms, ended by its `->`. A `(` type there stops
// before an `->`, so `fn (A) -> B` keeps its reading: a lambda taking an
// `A` whose body is `B`.
func TestLambdaParam_TypeOnlyForms(t *testing.T) {
	for _, tc := range []struct {
		src string
		i   int
		typ string
	}{
		{"(fn Vector<Int>, i: Int -> i)", 0, "Vector<Int>"},
		{"(fn i: Int, Vector<Int> -> i)", 1, "Vector<Int>"},
		{"(fn http.Request -> 1)", 0, "http.Request"},
		{"(fn (Int, String) -> 1)", 0, "(Int, String)"},
		{"(fn (Int) -> Int)", 0, "Int"},
	} {
		p := lambdaParam(t, tc.src, tc.i)
		if !p.TypeOnly || p.TypeAnnotation == nil || p.TypeAnnotation.TypeString() != tc.typ {
			t.Errorf("%q: param %d = %+v, want TypeOnly %s", tc.src, tc.i, p, tc.typ)
		}
	}
	expr := parseExpr(t, "(fn (Int) -> Int)")
	if g, ok := expr.(*ast.GroupedExpr); ok {
		expr = g.Expr
	}
	if lam, ok := expr.(*ast.Lambda); !ok || len(lam.Params) != 1 {
		t.Fatalf("fn (Int) -> Int = %T, want a lambda of one parameter", expr)
	}
	if p := lambdaParam(t, "(fn (a, b) -> a)", 0); p.TypeOnly || p.Destructure == nil {
		t.Errorf("fn (a, b): param = %+v, want a tuple pattern", p)
	}
}
