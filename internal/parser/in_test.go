package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `in` binds as `==` does: tighter than `not`, `and` and `or`, looser than
// arithmetic, ranges, the bitwise operators and `|>`. Its left operand is
// the value and its right the collection.
func TestIn_Precedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x in xs", "(x in xs)"},
		{"x in 1..10", "(x in *ast.RangeLit)"},
		{"x in 1..=10", "(x in *ast.RangeLit)"},
		{"not id in seen", "(not (id in seen))"},
		{"x in a and y", "((x in a) and y)"},
		{"x in a or y in b", "((x in a) or (y in b))"},
		{"a and x in s", "(a and (x in s))"},
		{"x + 1 in s", "((x + 1) in s)"},
		{"x in a ||| b", "(x in (a ||| b))"},
		{"x in xs |> Iter.to_set()", "(x in (xs |> Iter.to_set()))"},
		{"xs |> List.head() in s", "((xs |> List.head()) in s)"},
		{"(x in s) == b", "([(x in s)] == b)"},
		{"a == (b in s)", "(a == [(b in s)])"},
		{"(a < b) in flags", "([(a < b)] in flags)"},
		{"x.id in seen", "(x.id in seen)"},
		{"f(x)! in s", "(f(x)! in s)"},
	} {
		if got := isShape(parseExpr(t, tc.src)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An `in` test works inside a capture's parentheses and a lambda's body,
// and a line that starts with `in` continues the one above, as one that
// starts with `==` does.
func TestIn_InCapturesAndAcrossLines(t *testing.T) {
	for _, src := range []string{
		"Iter.reject(&(&1 in composites))",
		"Iter.filter(fn x -> x in seen)",
	} {
		call, ok := parseExpr(t, src).(*ast.Call)
		if !ok || len(call.Args) != 1 {
			t.Fatalf("%s: parsed %#v", src, call)
		}
		found := false
		ast.Inspect(call.Args[0], func(n ast.Node) bool {
			if b, ok := n.(*ast.Binary); ok && b.Op == "in" {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s: no `in` test in the argument", src)
		}
	}
	prog, err := Parse(lexer.Lex("y = x\n    in seen\n"))
	if err != nil {
		t.Fatalf("a line starting with `in`: %v", err)
	}
	if b, ok := prog[0].(*ast.Binding); !ok || isShape(b.Value) != "(x in seen)" {
		t.Errorf("a line starting with `in` is not the test's: %#v", prog[0])
	}
}

// `in` does not chain with a comparison, `is` or another `in`, on either
// side, and there is no `not in`.
func TestIn_Errors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"y = a == b in s\n", "`in` does not chain with a comparison"},
		{"y = a < b in s\n", "`in` does not chain with a comparison"},
		{"y = x in s == b\n", "`in` does not chain with `==`"},
		{"y = x in s != b\n", "`in` does not chain with `!=`"},
		{"y = x in a < b\n", "`in` does not chain with `<`"},
		{"y = x in a >= b\n", "`in` does not chain with `>=`"},
		{"y = x in a in b\n", "`in` does not chain with another `in`"},
		{"y = x in s is True\n", "`in` does not chain with `is`"},
		{"y = x is .A in s\n", "`is` does not chain with `in`"},
		{"y = x not in s\n", "`not in` is not an operator"},
		{"y = x in\n", "expected a collection after `in`"},
		{"y = in s\n", "`in` needs an operand before it"},
	} {
		_, err := Parse(lexer.Lex(tc.src))
		if err == nil {
			t.Errorf("%q parses; want the error %q", tc.src, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %q, want it to contain %q", tc.src, err, tc.want)
		}
	}
	// The hints ask for parentheses, or `not x in xs`.
	for _, tc := range []struct{ src, want string }{
		{"y = x in s == b\n", "`(x in xs) == ...`"},
		{"y = a == b in s\n", "`(a == b) in flags`"},
		{"y = x not in s\n", "write `not x in xs`"},
	} {
		_, err := Parse(lexer.Lex(tc.src))
		pe, ok := err.(ParseError)
		if !ok || !strings.Contains(strings.Join(pe.Hints, "\n"), tc.want) {
			t.Errorf("%q: error %#v, want a hint containing %q", tc.src, err, tc.want)
		}
	}
}

// `in` is a keyword, so it names nothing; `index` and `inner` are names.
func TestIn_IsAKeyword(t *testing.T) {
	for _, src := range []string{"in = 1\n", "fn f(in: Int) -> Int { in }\n"} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("%q parses", src)
		}
	}
	for _, src := range []string{"index = 1\n", "inner = 1\n", "import std/in\n"} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}
