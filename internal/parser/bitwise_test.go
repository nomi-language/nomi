package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// grouping renders an expression with every operator's operands in
// parentheses, so a test reads the precedence the parser chose.
func grouping(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Binary:
		return "(" + grouping(v.Left) + " " + v.Op + " " + grouping(v.Right) + ")"
	case *ast.Unary:
		return "(" + v.Op + grouping(v.Right) + ")"
	case *ast.GroupedExpr:
		return grouping(v.Expr)
	case *ast.Ident:
		return v.Name
	case *ast.IntLit:
		return fmt.Sprint(v.Value)
	case *ast.Call:
		return grouping(v.Func) + "(...)"
	case *ast.FieldAccess:
		return grouping(v.Object) + "." + v.Field.Name
	case *ast.TypeIdent:
		return v.Name
	}
	return fmt.Sprintf("%T", n)
}

// The bitwise operators bind as Go's do: `&&&` and the shifts like `*`,
// `|||` and `^^^` like `+`, `~~~` like unary `-`, all below calls and
// above ranges, the pipe, comparison and the logical words.
func TestBitwiseOperatorPrecedence(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"byte >>> (7 - i) &&& 1", "((byte >>> (7 - i)) &&& 1)"},
		{"data <<< 1 + parity", "((data <<< 1) + parity)"},
		{"x &&& mask == 0", "((x &&& mask) == 0)"},
		{"a ||| b &&& c", "(a ||| (b &&& c))"},
		{"a ^^^ b ||| c", "((a ^^^ b) ||| c)"},
		{"a ||| b + c", "((a ||| b) + c)"},
		{"a * b <<< c", "((a * b) <<< c)"},
		{"a <<< b * c", "((a <<< b) * c)"},
		{"~~~a &&& b", "((~~~a) &&& b)"},
		{"~~~f(x)", "(~~~f(...))"},
		{"-a >>> 1", "((-a) >>> 1)"},
		{"a >>> b >>> c", "((a >>> b) >>> c)"},
		{"x &&& 1 < y ||| 2", "((x &&& 1) < (y ||| 2))"},
		{"x ||| y and z", "((x ||| y) and z)"},
	} {
		if got := grouping(parseExpr(t, c.src)); got != c.want {
			t.Errorf("%s parses as %s, want %s", c.src, got, c.want)
		}
	}
}

// A pipe takes the bitwise operators' result, as it takes `+`'s.
func TestBitwiseOperatorsBindTighterThanAPipe(t *testing.T) {
	got := grouping(parseExpr(t, "a &&& b |> f()"))
	if got != "((a &&& b) |> f(...))" {
		t.Errorf("a &&& b |> f() parses as %s", got)
	}
}

// `>>>` is three `>` tokens, so the `>>>` and `>>` that close nested type
// arguments in a call, an annotation and a declaration close them.
func TestNestedTypeArgumentsCloseWithAdjacentAngles(t *testing.T) {
	src := strings.Join([]string{
		"type Deep List<List<List<Int>>>",
		"",
		"type Pair Map<String, List<Int>>",
		"",
		"struct Holder {",
		"    m: Map<String, List<List<Int>>>",
		"    n: List<List<Int>>",
		"}",
		"",
		"fn f(x: Map<String, List<List<Int>>>) -> List<List<List<Int>>> {",
		"    y: List<List<List<Int>>> = Iter.to_list<List<List<Int>>>(x)",
		"    z = Iter.to_map<String, List<List<Int>>>(y)",
		"    w: Map<String, List<Int>> = Iter.to_map<String, List<Int>>(z)",
		"    v = w |> Iter.to_list<List<List<Int>>>()",
		"    v >>> 1",
		"}",
		"",
	}, "\n")
	if _, err := Parse(lexer.Lex(src)); err != nil {
		t.Fatalf("nested type arguments do not parse: %v", err)
	}
}

// After an operand, `>>>` is the shift even beside a type-argument close.
func TestShiftRightAfterATurbofishCall(t *testing.T) {
	got := grouping(parseExpr(t, "Iter.count<List<Int>>(x) >>> 1"))
	if got != "(Iter.count(...) >>> 1)" {
		t.Errorf("parses as %s", got)
	}
}

// The C-family shifts and a spaced-out `>>>` are errors that name Nomi's
// spelling or the stray token.
func TestCFamilyShiftsAreErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"x = a >> 1\n", "Nomi spells shift right `>>>`"},
		{"x = a << 1\n", "Nomi spells shift left `<<<`"},
		{"x = ~a\n", "Nomi spells bitwise not `~~~`"},
		{"x = a && b\n", "Nomi spells logical and `and`"},
		{"x = a || b\n", "Nomi spells logical or `or`"},
	} {
		err := parseError(t, c.src)
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not say %q", c.src, err, c.want)
		}
	}
}

// A line that starts with a binary bitwise operator continues the
// expression above it, as one starting with `<` or `and` does.
func TestBitwiseOperatorAtALineStartContinues(t *testing.T) {
	nodes := parse(t, "x = a\n    &&& b\n    ||| c\n    >>> 1\n    <<< 2\n    ^^^ d\n")
	if len(nodes) != 1 {
		t.Fatalf("got %d statements, want 1", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("got %T, want a binding", nodes[0])
	}
	if got, want := grouping(b.Value), "(((a &&& b) ||| ((c >>> 1) <<< 2)) ^^^ d)"; got != want {
		t.Errorf("parses as %s, want %s", got, want)
	}
}
