package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// shapeOf lists, in source order, the tuple, map, lambda and function
// declaration nodes of src, so a test reads which construct each spelling
// parsed as.
func shapeOf(t *testing.T, src string) string {
	t.Helper()
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	var out []string
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TupleLit:
				out = append(out, "tuple")
			case *ast.TuplePattern:
				out = append(out, "tuple-pattern")
			case *ast.TupleDestructure:
				out = append(out, "tuple-destructure")
			case *ast.MapLit:
				switch {
				case n.Spread != nil:
					out = append(out, "map-update")
				case n.TypeName != nil:
					out = append(out, "typed-map")
				default:
					out = append(out, "map")
				}
			case *ast.MapPattern:
				out = append(out, "map-pattern")
			case *ast.Lambda:
				out = append(out, "lambda")
			case *ast.FuncDef:
				out = append(out, "fn "+n.Name)
			}
			return true
		})
	}
	return strings.Join(out, " ")
}

func wantShape(t *testing.T, src, want string) {
	t.Helper()
	if got := shapeOf(t, src); got != want {
		t.Errorf("Parse(%q) = %q, want %q", src, got, want)
	}
}

func wantParseError(t *testing.T, src, want string) {
	t.Helper()
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatalf("Parse(%q): the parser accepts it, want an error containing %q", src, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("Parse(%q) error = %q, want it to contain %q", src, err.Error(), want)
	}
}

func TestHashTuples(t *testing.T) {
	wantShape(t, "x = #(1, \"a\")", "tuple")
	wantShape(t, "x = #(#(1, 2), [#(3, 4)])", "tuple tuple tuple")
	wantShape(t, "#(a, b) = pair", "tuple-destructure")
	wantShape(t, "fn f(#(a, b): #(Int, Int)) -> Int { a + b }", "fn f tuple-pattern")
	wantShape(t, "x = case p {\n    #(0, y) -> y\n    _ -> 0\n}", "tuple-pattern")
	// Parentheses only group.
	wantShape(t, "x = (1 + 2) * 3", "")
	wantShape(t, "x = case n {\n    (1) -> 0\n    _ -> 1\n}", "")
}

func TestHashTupleType(t *testing.T) {
	nodes := parse(t, "fn f() -> #(Int, String) { #(1, \"a\") }")
	fd := nodes[0].(*ast.FuncDef)
	ft, ok := fd.ReturnTypeExpr.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("return type = %#v, want a two-element tuple type", fd.ReturnTypeExpr)
	}
	if got := ft.TypeString(); got != "#(Int, String)" {
		t.Errorf("TypeString = %q", got)
	}
}

// A parenthesized list is an ordinary parse error: no message names tuples.
func TestParenthesizedListIsAnOrdinaryError(t *testing.T) {
	for _, src := range []string{"x = (1, 2)", "(a, b) = pair", "fn f() -> (Int, String) { x }"} {
		_, err := Parse(lexer.Lex(src))
		if err == nil {
			t.Errorf("Parse(%q): the parser accepts it", src)
			continue
		}
		if strings.Contains(err.Error(), "tuple") || strings.Contains(err.Error(), "#(") {
			t.Errorf("Parse(%q) error %q mentions tuples; it should be an ordinary error", src, err.Error())
		}
	}
}

func TestHashTupleNeedsTwoItems(t *testing.T) {
	wantParseError(t, "x = #(1)", "tuple")
	wantParseError(t, "x = #()", "tuple")
}

func TestPercentMaps(t *testing.T) {
	wantShape(t, "m = %{\"a\" => 1, \"b\" => 2}", "map")
	wantShape(t, "m = %{}", "map")
	wantShape(t, "m = %{..base, \"a\" => 1}", "map-update")
	wantShape(t, "m = %{..teams, \"hawks\" => %{.., \"city\" => \"Atlanta\"}}", "map-update map-update")
	wantShape(t, "m = Kvs%{\"a\" => 1}", "typed-map")
	wantShape(t, "x = case m {\n    %{\"k\" => v} -> v\n    _ -> 0\n}", "map-pattern")
	wantShape(t, "%{\"k\" => v} = m else { return 0 }", "map-pattern")
}

// A brace never opens a map, and `%` opens one only when `{` follows it
// directly.
func TestABraceIsNeverAMap(t *testing.T) {
	_, err := Parse(lexer.Lex("m = {\"a\" => 1}"))
	if err == nil {
		t.Errorf("a brace holding `=>` parses")
	}
	wantShape(t, "x = a % {b: 1}.b", "")
}

func TestLambdaPositions(t *testing.T) {
	for _, src := range []string{
		"f = fn x -> x + 1",
		"f = fn -> 1",
		"f = fn x, y -> x + y",
		"f = fn x: Int = 1 -> x",
		"f = fn #(a, b) -> a + b",
		"f = fn Duration(ns) -> ns",
		"f = fn {x, y} -> x + y",
		"f = fn x -> {\n    x + 1\n}",
		"_ = g(fn x -> x)",
		"_ = g(1, fn x -> x, 2)",
		"_ = g(by: fn x -> x)",
		"_ = [fn x -> x, fn y -> y]",
		"_ = #(fn x -> x, 1)",
		"_ = %{\"inc\" => fn x -> x + 1}",
		"_ = Box{f: fn x -> x}",
		"_ = xs |> Iter.map(fn x -> x * 2)",
		"_ = 1 |> then fn v -> v + 1",
		"_ = 1 |> tap fn v -> io.print(v)",
		"_ = case n {\n    0 -> fn x -> x\n    _ -> fn x -> x + 1\n}",
		"_ = if c { fn x -> x } else { fn x -> 0 }",
	} {
		if got := shapeOf(t, src); !strings.Contains(got, "lambda") {
			t.Errorf("Parse(%q) = %q, want a lambda", src, got)
		}
	}
}

// A lambda whose parameter has a function type: the type's `->` belongs to
// the type, and the lambda's own arrow comes after it.
func TestLambdaParameterOfFunctionType(t *testing.T) {
	nodes := parse(t, "g = fn f: (Int) -> Int -> f(1)")
	lam := nodes[0].(*ast.Binding).Value.(*ast.Lambda)
	if len(lam.Params) != 1 || lam.Params[0].Name != "f" {
		t.Fatalf("params = %+v", lam.Params)
	}
	if got := lam.Params[0].TypeAnnotation.TypeString(); got != "(Int) -> Int" {
		t.Errorf("parameter type = %q, want (Int) -> Int", got)
	}
}

// A lambda's body ends at the delimiter that closes the place it stands in.
func TestLambdaBodyBoundaries(t *testing.T) {
	nodes := parse(t, "_ = g(fn x -> x + 1, 2)")
	call := nodes[0].(*ast.Binding).Value.(*ast.Call)
	if len(call.Args) != 2 {
		t.Fatalf("args = %d, want the lambda and 2", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.Lambda); !ok {
		t.Errorf("first argument = %T, want a lambda", call.Args[0])
	}
	if got := shapeOf(t, "_ = [fn x -> x, 3]"); got != "lambda" {
		t.Errorf("list = %q, want one lambda and a number", got)
	}
}

// `fn` followed by a name and `(` or `<` declares a function; anything else
// after `fn` is a lambda.
func TestDeclarationOrLambda(t *testing.T) {
	wantShape(t, "fn f(x: Int) -> Int { x }", "fn f")
	wantShape(t, "fn id<T>(x: T) -> T { x }", "fn id")
	wantShape(t, "fn main() {\n    fn helper(n: Int) -> Int { n * 2 }\n    g = fn n -> helper(n)\n    _ = g(1)\n}",
		"fn main fn helper lambda")
	wantShape(t, "fn main() {\n    fn x -> x\n}", "fn main lambda")
	wantShape(t, "fn main() {\n    fn -> 1\n}", "fn main lambda")
}

func TestReturnArrow(t *testing.T) {
	nodes := parse(t, "fn make() -> (Int) -> Int { fn x -> x }")
	fd := nodes[0].(*ast.FuncDef)
	if got := fd.ReturnTypeExpr.TypeString(); got != "(Int) -> Int" {
		t.Errorf("return type = %q, want (Int) -> Int", got)
	}
	if got := shapeOf(t, "fn make() -> (Int) -> Int { fn x -> x }"); got != "fn make lambda" {
		t.Errorf("body = %q", got)
	}

	nodes = parse(t, "fn shown<T>(x: T) -> String where T: Display { Display.to_string(x) }")
	fd = nodes[0].(*ast.FuncDef)
	if fd.ReturnTypeExpr.TypeString() != "String" || len(fd.WhereClauses) != 1 {
		t.Errorf("return %q, %d where clauses", fd.ReturnTypeExpr.TypeString(), len(fd.WhereClauses))
	}

	nodes = parse(t, "interface Named {\n    fn name(value: self) -> String\n}")
	id := nodes[0].(*ast.InterfaceDef)
	if len(id.Methods) != 1 || id.Methods[0].ReturnTypeExpr.TypeString() != "String" {
		t.Errorf("interface requirement = %+v", id.Methods)
	}

	parse(t, "pub host fn now() -> Int")
	parse(t, "fn up(s: String) -> String go strings.ToUpper")
	parse(t, "export fn add(a: Int, b: Int) -> Int { a + b }")
}

// The spellings these replace are ordinary parse errors.
func TestOldSpellingsAreErrors(t *testing.T) {
	for _, src := range []string{
		"fn f(x: Int): Int { x }",
		"interface I {\n    fn name(value: self): String\n}",
		"f = |x| x + 1",
		"m = {\"a\" => 1}",
		"x = (1, 2)",
	} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("Parse(%q): the parser accepts it", src)
		}
	}
}
