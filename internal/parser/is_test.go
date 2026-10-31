package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// isShape is notShape with an `is` test written `{value is Pattern}`, the
// pattern by its node type.
func isShape(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Is:
		return "{" + isShape(v.Value) + " is " + v.Pattern.NodeType() + "}"
	case *ast.Unary:
		return "(" + v.Op + " " + isShape(v.Right) + ")"
	case *ast.Binary:
		return "(" + isShape(v.Left) + " " + v.Op + " " + isShape(v.Right) + ")"
	case *ast.GroupedExpr:
		return "[" + isShape(v.Expr) + "]"
	}
	return notShape(n)
}

// `is` binds as `==` does: tighter than `not`, `and` and `or`, looser than
// arithmetic, ranges and `|>`.
func TestIs_Precedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"x is .A", "{x is EnumPattern}"},
		{"x is Some(_)", "{x is EnumPattern}"},
		{"not x is .A", "(not {x is EnumPattern})"},
		{"not block is .Item(_)", "(not {block is EnumPattern})"},
		{"x is .A and y", "({x is EnumPattern} and y)"},
		{"x is .A or y", "({x is EnumPattern} or y)"},
		{"a and x is .A", "(a and {x is EnumPattern})"},
		{"x is .A or y is .B", "({x is EnumPattern} or {y is EnumPattern})"},
		{"xs |> List.head() is Some(_)", "{(xs |> List.head()) is EnumPattern}"},
		{"a + 1 is 2", "{(a + 1) is IntLit}"},
		{"a &&& 1 is 0", "{(a &&& 1) is IntLit}"},
		{"x.kind is .A", "{x.kind is EnumPattern}"},
		{"f(x)! is .A", "{f(x)! is EnumPattern}"},
		{"kind is (.Circle or .Square)", "{kind is OrPattern}"},
		{"(x is .A) == b", "([{x is EnumPattern}] == b)"},
		{"a == (x is .A)", "(a == [{x is EnumPattern}])"},
		{"(a < b) is True", "{[(a < b)] is EnumPattern}"},
		{"x is -1", "{x is IntLit}"},
		{"x is \"/u/\" + _rest", "{x is Binary}"},
		{"x is [_, ..]", "{x is ListPattern}"},
		{"x is #(_, 1)", "{x is TuplePattern}"},
		{"x is %{\"k\" => _}", "{x is MapPattern}"},
		{"x is shape.Shape.Circle", "{x is EnumPattern}"},
	} {
		if got := isShape(parseExpr(t, tc.src)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An `is` test works inside a capture's parentheses and a lambda's body,
// and a line that starts with `is` continues the one above, as one that
// starts with `==` does.
func TestIs_InCapturesAndAcrossLines(t *testing.T) {
	for _, src := range []string{
		"Iter.filter(&(&1 is .Item(_)))",
		"Iter.filter(fn b -> b is .Item(_))",
	} {
		call, ok := parseExpr(t, src).(*ast.Call)
		if !ok || len(call.Args) != 1 {
			t.Fatalf("%s: parsed %#v", src, call)
		}
		found := false
		ast.Inspect(call.Args[0], func(n ast.Node) bool {
			if _, ok := n.(*ast.Is); ok {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s: no `is` test in the argument", src)
		}
	}
	prog, err := Parse(lexer.Lex("y = x\n    is .A\n"))
	if err != nil {
		t.Fatalf("a line starting with `is`: %v", err)
	}
	if b, ok := prog[0].(*ast.Binding); !ok || isShape(b.Value) != "{x is EnumPattern}" {
		t.Errorf("a line starting with `is` is not the test's: %#v", prog[0])
	}
}

// The errors: a bare `or` followed by what only a pattern can be, a chain
// with a comparison or another `is`, an operator after the pattern, `is
// not`, and a missing pattern or value.
func TestIs_Errors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"y = x is .A or .B\n", "this `or` joins two Bool values, and a pattern cannot be its right operand: `is` takes one pattern"},
		{"y = x is .A(_) or .B(_)\n", "a pattern cannot be its right operand"},
		{"y = x is Some(_) or _\n", "a pattern cannot be its right operand"},
		{"y = a and x is .A or .B\n", "a pattern cannot be its right operand"},
		{"y = x is .A or #.B{r: 1}\n", "a pattern cannot be its right operand"},
		{"y = a == b is True\n", "`is` does not chain with a comparison"},
		{"y = a < b is True\n", "`is` does not chain with a comparison"},
		{"y = x is .A == b\n", "`is` does not chain with `==`"},
		{"y = x is .A != b\n", "`is` does not chain with `!=`"},
		{"y = x is .A is .B\n", "`is` does not chain with another `is`"},
		{"y = x is .A |> f()\n", "an `is` test's pattern ends before `|>`"},
		{"y = x is 1 + 1\n", "an `is` test's pattern ends before `+`"},
		{"y = x is not .A\n", "`is not` is not an operator"},
		{"y = x is\n", "expected a pattern after `is`"},
		{"y = is .A\n", "`is` needs an operand before it"},
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
}

// `is` is a keyword, so it names nothing; `is_empty` is a name.
func TestIs_IsAKeyword(t *testing.T) {
	if _, err := Parse(lexer.Lex("is = 1\n")); err == nil {
		t.Errorf("`is = 1` parses")
	}
	if _, err := Parse(lexer.Lex("is_empty = 1\n")); err != nil {
		t.Errorf("`is_empty = 1`: %v", err)
	}
}
