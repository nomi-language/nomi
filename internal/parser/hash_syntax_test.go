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
				case len(n.Spreads) > 0:
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

func TestHashedMaps(t *testing.T) {
	wantShape(t, "m = %{\"a\" => 1, \"b\" => 2}", "map")
	wantShape(t, "m = %{}", "map")
	wantShape(t, "m = %{..base, \"a\" => 1}", "map-update")
	wantShape(t, "m = %{..teams, \"hawks\" => %{.., \"city\" => \"Atlanta\"}}", "map-update map-update")
	wantShape(t, "m = %Kvs{\"a\" => 1}", "typed-map")
	wantShape(t, "x = case m {\n    %{\"k\" => v} -> v\n    _ -> 0\n}", "map-pattern")
	wantShape(t, "%{\"k\" => v} = m else { return 0 }", "map-pattern")
}

// A plain brace never opens a map, and `%` with a space after it is the
// remainder operator.
func TestABraceIsNeverAMap(t *testing.T) {
	_, err := Parse(lexer.Lex("m = {\"a\" => 1}"))
	if err == nil {
		t.Errorf("a brace holding `=>` parses")
	}
	wantShape(t, "x = a % #{b: 1}.b", "")
	wantShape(t, "x = a % b", "")
	wantShape(t, "x = a % (b)", "")
}

func TestLambdaPositions(t *testing.T) {
	for _, src := range []string{
		"f = fn x -> x + 1",
		"f = fn -> 1",
		"f = fn x, y -> x + y",
		"f = fn x: Int = 1 -> x",
		"f = fn #(a, b) -> a + b",
		"f = fn Duration(ns) -> ns",
		"f = fn #{x, y} -> x + y",
		"f = fn #[x, ..] -> x",
		"f = fn %{\"k\" => v} -> v",
		"f = fn x -> {\n    x + 1\n}",
		"_ = g(fn x -> x)",
		"_ = g(1, fn x -> x, 2)",
		"_ = g(by: fn x -> x)",
		"_ = [fn x -> x, fn y -> y]",
		"_ = #(fn x -> x, 1)",
		"_ = %{\"inc\" => fn x -> x + 1}",
		"_ = #Box{f: fn x -> x}",
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
		"s = #{1, 2}",
		"p = {x: 1}",
		"fn f(p: {x: Int}) -> Int { p.x }",
	} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("Parse(%q): the parser accepts it", src)
		}
	}
}

// literalKinds lists, in source order, the collection and struct literal,
// pattern and type nodes of src, and the blocks.
func literalKinds(t *testing.T, src string) string {
	t.Helper()
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	var out []string
	named := func(kind string, name ast.TypeExpr) string {
		if name != nil {
			return kind + " " + name.TypeString()
		}
		return kind
	}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.VectorLit:
				if len(n.Spreads) > 0 {
					out = append(out, "vector-spread")
				} else {
					out = append(out, named("vector", n.TypeName))
				}
			case *ast.SetLit:
				if len(n.Spreads) > 0 {
					out = append(out, "set-spread")
				} else {
					out = append(out, named("set", n.TypeName))
				}
			case *ast.MapLit:
				out = append(out, named("map", n.TypeName))
			case *ast.TupleLit:
				out = append(out, named("tuple", n.TypeName))
			case *ast.MapPattern:
				out = append(out, named("map-pattern", n.TypeName))
			case *ast.VectorPattern:
				out = append(out, named("vector-pattern", n.TypeName))
			case *ast.SetPattern:
				out = append(out, named("set-pattern", n.TypeName))
			case *ast.TuplePattern:
				out = append(out, named("tuple-pattern", n.TypeName))
			case *ast.StructLit:
				switch {
				case n.TypeName != nil:
					out = append(out, "struct "+n.TypeName.TypeString())
				case n.Spread != nil:
					out = append(out, "struct-update")
				default:
					out = append(out, "struct")
				}
			case *ast.StructPattern:
				out = append(out, named("struct-pattern", n.TypeName))
			case *ast.StructDestructure:
				out = append(out, "struct-destructure")
			case *ast.AnonStructType:
				out = append(out, "struct-type")
			case *ast.ListLit:
				if n.TypeName != nil {
					out = append(out, "list "+n.TypeName.TypeString())
				}
			case *ast.EnumPattern:
				out = append(out, "variant-pattern "+n.Variant.TypeString())
			case *ast.Block:
				out = append(out, "block")
			}
			return true
		})
	}
	return strings.Join(out, " ")
}

func wantKinds(t *testing.T, src, want string) {
	t.Helper()
	if got := literalKinds(t, src); got != want {
		t.Errorf("Parse(%q) = %q, want %q", src, got, want)
	}
}

// Every literal of a built-in shape starts with that shape's symbol: `#` for
// the indexed and product shapes (vector, struct, tuple), `%` for the hashed
// ones (map, set).
func TestShapeSymbolLiterals(t *testing.T) {
	wantKinds(t, "v = #[1, 2]", "vector")
	wantKinds(t, "v = #[]", "vector")
	wantKinds(t, "v = #[..xs, 3]", "vector-spread")
	wantKinds(t, "v = #[3, ..xs]", "vector-spread")
	wantKinds(t, "s = %[38, 89]", "set")
	wantKinds(t, "s = %[]", "set")
	wantKinds(t, "s = %[..s, x]", "set-spread")
	wantKinds(t, "m = %{\"hello\" => 7}", "map")
	wantKinds(t, "m = %{}", "map")
	wantKinds(t, "p = #{x: 1, y: 2}", "struct")
	wantKinds(t, "p = #{}", "struct")
	wantKinds(t, "p = #{..base, x: 1}", "struct-update")
	wantKinds(t, "t = #(1, 2)", "tuple")
	// A lone punned field is a struct: a block opens with a plain `{`.
	wantKinds(t, "p = #{name}", "struct")
	wantKinds(t, "p = {name}", "block")
	wantKinds(t, "x = {\n    y = 1\n    y\n}", "block")
	// A list keeps its brackets.
	wantKinds(t, "l = Ids[1, 2]", "list Ids")
	wantKinds(t, "l = .Items[1, 2]", "list .Items")
}

// A type built on a shape writes its name right after the shape's symbol,
// and the token after the name says the shape.
func TestNamedShapeLiterals(t *testing.T) {
	wantKinds(t, "p = #Point{x: 1}", "struct Point")
	wantKinds(t, "p = #Point{}", "struct Point")
	wantKinds(t, "p = #Point{x}", "struct Point")
	wantKinds(t, "p = #geo.Point{x: 1}", "struct geo.Point")
	wantKinds(t, "c = #Shape.Circle{radius: 1.0}", "struct Shape.Circle")
	wantKinds(t, "c = #geo.Shape.Circle{radius: 1.0}", "struct geo.Shape.Circle")
	wantKinds(t, "c: Shape = #.Circle{radius: 1.0}", "struct .Circle")
	wantKinds(t, "c = #Circle{radius: 1.0}", "struct Circle")
	wantKinds(t, "p = #Pair(1, \"x\")", "tuple Pair")
	wantKinds(t, "v = #Path[1, 2]", "vector Path")
	wantKinds(t, "v: Bag = #.Items[1, 2]", "vector .Items")
	wantKinds(t, "k = %Kvs{\"a\" => 4}", "map Kvs")
	wantKinds(t, "k = %Kvs{}", "map Kvs")
	wantKinds(t, "j: Json = %.Obj{\"k\" => v}", "map .Obj")
	wantKinds(t, "j = %Json.Obj{\"k\" => v}", "map Json.Obj")
	wantKinds(t, "b = %shapes.Bag.Named{\"a\" => 4}", "map shapes.Bag.Named")
	wantKinds(t, "t = %Tags[\"a\", \"b\"]", "set Tags")
	wantKinds(t, "p = #Point{x: %{1 => 2}}", "struct Point map")
	wantKinds(t, "p = #Point{f: fn x -> case x {\n    1 -> 2\n    _ -> 3\n}}", "struct Point block")
	wantParseError(t, "k = %Kvs{..m, \"a\" => 1}", "map spread `..` has no named form; write `%{..base, key => value}`")
	wantParseError(t, "p = #Point{..q, x: 1}", "struct spread `..` has no named form; write `#{..base, field: value}`")
	wantParseError(t, "v = #Path[..v, 1]", "vector spread `..` has no named form; write `#[..base, item]`")
	wantParseError(t, "s = %Tags[..s, 1]", "set spread `..` has no named form; write `%[..base, item]`")
	wantParseError(t, "p = #Point", "expected '{', '(' or '[' after `#Point`")
	wantParseError(t, "p = #", "expected '{', '(', '[' or a type name after '#'")
}

// A struct literal starts with `#`, so a `{` after an `if` condition, a
// `case` subject or a `subtests` name always opens the body, and a struct
// literal stands in each of them as anywhere else.
func TestStructLiteralsInConditions(t *testing.T) {
	wantKinds(t, "x = if p == #Point{x: 1} { 1 } else { 2 }", "struct Point block block")
	wantKinds(t, "x = case #Point{x: 1} {\n    #Point{x} -> x\n}", "struct Point struct-pattern Point")
	wantKinds(t, "x = if c == %{1 => 2} { 1 } else { 2 }", "map block block")
	wantKinds(t, "x = if c { 1 } else { 2 }", "block block")
	wantKinds(t, "x = if True { 1 } else { 2 }", "block block")
}

func TestShapeSymbolPatternsAndTypes(t *testing.T) {
	wantKinds(t, "x = case p {\n    #{x, y} -> x\n    _ -> 0\n}", "struct-pattern")
	wantKinds(t, "x = case p {\n    #{x: 0, y} -> y\n    _ -> 0\n}", "struct-pattern")
	wantKinds(t, "#{x, y} = p", "struct-destructure")
	wantKinds(t, "#Point{x, y} = p", "struct-pattern Point")
	wantKinds(t, "fn f(#{x, y}: Point) -> Int { x }", "struct-pattern block")
	wantKinds(t, "fn f(#Point{x, y}) -> Int { x }", "struct-pattern Point block")
	wantKinds(t, "fn f(p: #{x: Int, y: List<#{z: Int}>}) -> Int { p.x }", "struct-type struct-type block")
	wantKinds(t, "x = case m {\n    %{\"k\" => v} -> v\n    _ -> 0\n}", "map-pattern")
	wantKinds(t, "x = case m {\n    %Kvs{\"k\" => v} -> v\n    _ -> 0\n}", "map-pattern Kvs")
	wantKinds(t, "x = case j {\n    %.Obj{\"k\" => v} -> v\n    _ -> 0\n}", "map-pattern .Obj")
	wantKinds(t, "x = case p {\n    #Point{x} -> x\n    _ -> 0\n}", "struct-pattern Point")
	wantKinds(t, "x = case s {\n    #.Circle{radius} -> radius\n    #Shape.Square{side} -> side\n}", "struct-pattern .Circle struct-pattern Shape.Square")
	wantKinds(t, "x = case p {\n    #Pair(a, b) -> a\n}", "tuple-pattern Pair")
	wantKinds(t, "#Pair(a, b) = p", "tuple-pattern Pair")
	wantKinds(t, "x = case p {\n    Pair(#(a, b)) -> a\n}", "variant-pattern Pair tuple-pattern")
}

func TestVectorPatterns(t *testing.T) {
	vec := func(src string) *ast.VectorPattern {
		t.Helper()
		var found *ast.VectorPattern
		nodes := parse(t, src)
		for _, n := range nodes {
			ast.Inspect(n, func(n ast.Node) bool {
				if v, ok := n.(*ast.VectorPattern); ok && found == nil {
					found = v
				}
				return true
			})
		}
		if found == nil {
			t.Fatalf("Parse(%q): no vector pattern", src)
		}
		return found
	}
	arms := func(pat string) string { return "x = case v {\n    " + pat + " -> 0\n    _ -> 1\n}" }
	for _, c := range []struct {
		pat          string
		heads, tails int
		spread       bool
		rest         string
	}{
		{"#[]", 0, 0, false, ""},
		{"#[a, b]", 2, 0, false, ""},
		{"#[first, ..rest]", 1, 0, true, "rest"},
		{"#[.., last]", 0, 1, true, ""},
		{"#[a, b, ..]", 2, 0, true, ""},
		{"#[a, ..mid, z]", 1, 1, true, "mid"},
		{"#[..all]", 0, 0, true, "all"},
	} {
		v := vec(arms(c.pat))
		rest := ""
		switch r := v.Rest.(type) {
		case *ast.IdentPattern:
			rest = r.Name
		case *ast.WildcardPattern:
			rest = "_"
		}
		if len(v.Heads) != c.heads || len(v.Tails) != c.tails || v.Spread != c.spread || rest != c.rest {
			t.Errorf("%s: heads %d tails %d spread %v rest %q", c.pat, len(v.Heads), len(v.Tails), v.Spread, rest)
		}
	}
	wantKinds(t, arms("#[#(a, b), ..rest]"), "vector-pattern tuple-pattern")
	wantKinds(t, arms("#[Some(x), ..]"), "vector-pattern variant-pattern Some")
	wantKinds(t, arms("#[a] or #[a, _]"), "vector-pattern vector-pattern")
	wantKinds(t, arms("#[a, ..] as whole"), "vector-pattern")
	wantKinds(t, arms("#Path[a, ..]"), "vector-pattern Path")
	wantKinds(t, "#[first, ..rest] = v else { return 0 }", "vector-pattern block")
	wantKinds(t, "x = if #[a, ..] = v { a } else { 0 }", "vector-pattern block block")
	wantKinds(t, "f = fn #[a, ..] -> a", "vector-pattern block")
	wantParseError(t, arms("#[a, .., b, ..]"), "a vector pattern takes one `..`")
	wantParseError(t, arms("#[..Some(x)]"), "the rest of a vector pattern is a name or nothing")
}

func TestSetPatterns(t *testing.T) {
	set := func(src string) *ast.SetPattern {
		t.Helper()
		var found *ast.SetPattern
		for _, n := range parse(t, src) {
			ast.Inspect(n, func(n ast.Node) bool {
				if v, ok := n.(*ast.SetPattern); ok && found == nil {
					found = v
				}
				return true
			})
		}
		if found == nil {
			t.Fatalf("Parse(%q): no set pattern", src)
		}
		return found
	}
	arms := func(pat string) string { return "x = case s {\n    " + pat + " -> 0\n    _ -> 1\n}" }
	for _, c := range []struct {
		pat    string
		elems  int
		spread bool
		rest   string
	}{
		{"%[]", 0, false, ""},
		{"%[1, 2]", 2, false, ""},
		{"%[\"admin\", ..rest]", 1, true, "rest"},
		{"%[\"admin\", ..]", 1, true, ""},
		{"%[role, ..]", 1, true, ""},
		{"%[..all]", 0, true, "all"},
	} {
		v := set(arms(c.pat))
		rest := ""
		if r, ok := v.Rest.(*ast.IdentPattern); ok {
			rest = r.Name
		}
		if len(v.Elems) != c.elems || v.Spread != c.spread || rest != c.rest {
			t.Errorf("%s: elems %d spread %v rest %q", c.pat, len(v.Elems), v.Spread, rest)
		}
	}
	// An element is an expression, the value the set must hold.
	if _, ok := set(arms("%[role, ..]")).Elems[0].(*ast.Ident); !ok {
		t.Errorf("a set pattern element is not parsed as an expression")
	}
	wantKinds(t, arms("%Tags[\"a\", ..]"), "set-pattern Tags")
	wantKinds(t, "%[\"a\", ..rest] = s else { return 0 }", "set-pattern block")
	wantParseError(t, arms("%[..rest, 1]"), "set pattern `..` must be the last element")
}

// A name followed by `{` never starts a literal or a pattern, and the
// spellings these replace are errors rather than meaning something else.
func TestOldCollectionSpellingsFail(t *testing.T) {
	for _, src := range []string{
		"p = Point{x: 1}",
		"p = Point{}",
		"c: Shape = .Circle{radius: 1.0}",
		"c = Shape.Circle{radius: 1.0}",
		"k = Kvs{\"a\" => 1}",
		"m = Map{\"a\" => 1}",
		"m = Map{}",
		"k = Kvs%{\"a\" => 1}",
		"x = case p {\n    Point{x} -> x\n    _ -> 0\n}",
		"x = case m {\n    Map{\"k\" => v} -> v\n    _ -> 0\n}",
		"x = case s {\n    .Circle{radius} -> radius\n    _ -> 0\n}",
		"Point{x, y} = p",
	} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("Parse(%q): the parser accepts it", src)
		}
	}
	// `Vector[...]` and `Set[...]` parse as list literals that name a type,
	// which the checker refuses: Vector and Set are not list types.
	wantKinds(t, "v = Vector[1, 2]", "list Vector")
	wantKinds(t, "s = Set[1, 2]", "list Set")
}

// A type over an anonymous struct is written as a struct.
func TestShapeSymbolRefusals(t *testing.T) {
	wantParseError(t, "type Wrap #{x: Int}", "a type over an anonymous struct is a struct: write `struct Wrap { ... }`")
	wantParseError(t, "s = #{1, 2}", "expected field name in struct literal")
	wantParseError(t, "s = % [1, 2]", "unexpected `%`: a map or set literal writes `%` right before its `{` or `[`")
}
