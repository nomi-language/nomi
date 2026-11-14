package parser

import (
	"errors"
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
	wantShape(t, "x = (1, \"a\")", "tuple")
	wantShape(t, "x = ((1, 2), [(3, 4)])", "tuple tuple tuple")
	wantShape(t, "(a, b) = pair", "tuple-destructure")
	wantShape(t, "fn f((a, b): (Int, Int)) -> Int\n    a + b\nend", "fn f tuple-pattern")
	wantShape(t, "x = case p\n    (0, y) -> y\n    _ -> 0\nend", "tuple-pattern")
	// Parentheses only group.
	wantShape(t, "x = (1 + 2) * 3", "")
	wantShape(t, "x = case n\n    (1) -> 0\n    _ -> 1\nend", "")
}

func TestHashTupleType(t *testing.T) {
	nodes := parse(t, "fn f() -> (Int, String)\n    (1, \"a\")\nend")
	fd := nodes[0].(*ast.FuncDef)
	ft, ok := fd.ReturnTypeExpr.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("return type = %#v, want a two-element tuple type", fd.ReturnTypeExpr)
	}
	if got := ft.TypeString(); got != "(Int, String)" {
		t.Errorf("TypeString = %q", got)
	}
}

// A tuple has no `#`: `#(` as a literal, a pattern or a type is an error
// that says so, with the `#` to drop as its fix.
func TestHashTupleNamesTheParenthesizedForm(t *testing.T) {
	for _, src := range []string{
		"x = #(1, 2)",
		"#(a, b) = pair",
		"fn f() -> #(Int, String) { x }",
		"x = case p\n    #(a, _) -> a\nend",
		"f = fn #(a, b) -> a",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil {
			t.Errorf("Parse(%q): the parser accepts it", src)
			continue
		}
		var pe ParseError
		if !errors.As(err, &pe) || !strings.Contains(pe.Message, "a tuple is written without `#`") {
			t.Errorf("Parse(%q) error %q does not say a tuple has no `#`", src, err.Error())
			continue
		}
		if pe.Fix == nil || len(pe.Fix.Edits) != 1 || pe.Fix.Edits[0].Text != "" ||
			pe.Fix.Edits[0].End.Col-pe.Fix.Edits[0].Start.Col != 1 {
			t.Errorf("Parse(%q) fix = %+v, want the `#` to drop", src, pe.Fix)
		}
	}
}

func TestTupleNeedsTwoItems(t *testing.T) {
	wantParseError(t, "x = (1,)", "tuple")
	wantParseError(t, "x = ()", "tuple")
	wantParseError(t, "x: (Int,) = y", "tuple")
	wantParseError(t, "x = case y\n    (a,) -> a\nend", "")
}

// One item in parentheses is that item, grouped: `(x)` is `x`.
func TestOneItemInParenthesesGroups(t *testing.T) {
	stmt := parse(t, "x = (1)")[0].(*ast.Binding)
	if _, ok := stmt.Value.(*ast.GroupedExpr); !ok {
		t.Errorf("(1) = %T, want a grouping", stmt.Value)
	}
	if _, ok := parse(t, "x = (1, 2)")[0].(*ast.Binding).Value.(*ast.TupleLit); !ok {
		t.Errorf("(1, 2) is not a tuple literal")
	}
}

func TestHashedMaps(t *testing.T) {
	wantShape(t, "m = {\"a\" => 1, \"b\" => 2}", "map")
	wantShape(t, "m = {}", "map")
	wantShape(t, "m = {..base, \"a\" => 1}", "map-update")
	wantShape(t, "m = {..teams, \"hawks\" => {.., \"city\" => \"Atlanta\"}}", "map-update map-update")
	wantShape(t, "m = Kvs{\"a\" => 1}", "typed-map")
	wantShape(t, "x = case m\n    {\"k\" => v} -> v\n    _ -> 0\nend", "map-pattern")
	wantShape(t, "{\"k\" => v} = m else\n    return 0\nend", "map-pattern")
}

func TestLambdaPositions(t *testing.T) {
	for _, src := range []string{
		"f = fn x -> x + 1",
		"f = fn -> 1",
		"f = fn x, y -> x + y",
		"f = fn x: Int = 1 -> x",
		"f = fn (a, b) -> a + b",
		"f = fn Duration(ns) -> ns",
		"f = fn {x:, y:} -> x + y",
		"f = fn [x, ..] -> x",
		"f = fn {\"k\" => v} -> v",
		"f = fn x ->\n    x + 1\nend",
		"_ = g(fn x -> x)",
		"_ = g(1, fn x -> x, 2)",
		"_ = g(by: fn x -> x)",
		"_ = [fn x -> x, fn y -> y]",
		"_ = (fn x -> x, 1)",
		"_ = {\"inc\" => fn x -> x + 1}",
		"_ = Box{f: fn x -> x}",
		"_ = xs |> Iter.map(fn x -> x * 2)",
		"_ = 1 |> then fn v -> v + 1",
		"_ = 1 |> tap fn v -> io.print(v)",
		"_ = case n\n    0 -> fn x -> x\n    _ -> fn x -> x + 1\nend",
		"_ = if c then (fn x -> x) else fn x -> 0",
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
	wantShape(t, "fn f(x: Int) -> Int\n    x\nend", "fn f")
	wantShape(t, "fn id<T>(x: T) -> T\n    x\nend", "fn id")
	wantShape(t, "fn main()\n    fn helper(n: Int) -> Int\n        n * 2\n    end\n    g = fn n -> helper(n)\n    _ = g(1)\nend",
		"fn main fn helper lambda")
	wantShape(t, "fn main()\n    fn x -> x\nend", "fn main lambda")
	wantShape(t, "fn main()\n    fn -> 1\nend", "fn main lambda")
}

func TestReturnArrow(t *testing.T) {
	nodes := parse(t, "fn make() -> (Int) -> Int\n    fn x -> x\nend")
	fd := nodes[0].(*ast.FuncDef)
	if got := fd.ReturnTypeExpr.TypeString(); got != "(Int) -> Int" {
		t.Errorf("return type = %q, want (Int) -> Int", got)
	}
	if got := shapeOf(t, "fn make() -> (Int) -> Int\n    fn x -> x\nend"); got != "fn make lambda" {
		t.Errorf("body = %q", got)
	}

	nodes = parse(t, "fn shown<T>(x: T) -> String where T: Display\n    Display.to_string(x)\nend")
	fd = nodes[0].(*ast.FuncDef)
	if fd.ReturnTypeExpr.TypeString() != "String" || len(fd.WhereClauses) != 1 {
		t.Errorf("return %q, %d where clauses", fd.ReturnTypeExpr.TypeString(), len(fd.WhereClauses))
	}

	nodes = parse(t, "interface Named\n    fn name(value: self) -> String\nend")
	id := nodes[0].(*ast.InterfaceDef)
	if len(id.Methods) != 1 || id.Methods[0].ReturnTypeExpr.TypeString() != "String" {
		t.Errorf("interface requirement = %+v", id.Methods)
	}

	parse(t, "pub host fn now() -> Int")
	parse(t, "fn up(s: String) -> String go strings.ToUpper")
	parse(t, "export fn add(a: Int, b: Int) -> Int\n    a + b\nend")
}

// The spellings these replace are ordinary parse errors.
func TestOldSpellingsAreErrors(t *testing.T) {
	for _, src := range []string{
		"fn f(x: Int): Int { x }",
		"interface I\n    fn name(value: self): String\nend",
		"f = |x| x + 1",
		"x = #(1, 2)",
		"s = {1, 2}",
		"p = .{x: 1}",
		"p = P{x}",
		"p = {x, y: 1}",
		"fn f(p: {}) -> Int { 1 }",
		"fn f(p: .{x: Int}) -> Int { p.x }",
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
			case *ast.ListLit:
				if len(n.Spreads) > 0 {
					out = append(out, named("bracket-spread", n.TypeName))
				} else {
					out = append(out, named("bracket", n.TypeName))
				}
			case *ast.MapLit:
				out = append(out, named("map", n.TypeName))
			case *ast.TupleLit:
				out = append(out, "tuple")
			case *ast.MapPattern:
				out = append(out, named("map-pattern", n.TypeName))
			case *ast.ListPattern:
				out = append(out, named("bracket-pattern", n.TypeName))
			case *ast.TuplePattern:
				out = append(out, "tuple-pattern")
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

// A collection literal has no symbol. Brackets are a List, a Vector or a
// Set, which the checker decides; braces are a map when the first entry is
// `key =>` or there is none, and a struct when it is `name:`.
func TestCollectionLiterals(t *testing.T) {
	wantKinds(t, "v = [1, 2]", "bracket")
	wantKinds(t, "v = []", "bracket")
	wantKinds(t, "v = [..xs, 3]", "bracket-spread")
	wantKinds(t, "v = [3, ..xs, ..ys]", "bracket-spread")
	wantKinds(t, "v = Vector[1, 2]", "bracket Vector")
	wantKinds(t, "v = Vector[..xs, 3]", "bracket-spread Vector")
	wantKinds(t, "s = Set[38, 89]", "bracket Set")
	wantKinds(t, "s = Set[..s, x]", "bracket-spread Set")
	wantKinds(t, "m = {\"hello\" => 7}", "map")
	wantKinds(t, "m = {key => 7}", "map")
	wantKinds(t, "m = {(1, 2) => 7, -1 => 0}", "map tuple")
	wantKinds(t, "m = {}", "map")
	wantKinds(t, "m = {..a, ..b}", "map")
	wantKinds(t, "m = {..a, \"k\" => 1}", "map")
	wantKinds(t, "m = Map{\"k\" => 1}", "map Map")
	wantKinds(t, "p = {x: 1, y: 2}", "struct")
	wantKinds(t, "p = {..base, x: 1}", "struct-update")
	wantKinds(t, "p = {..base}", "struct-update")
	wantKinds(t, "t = (1, 2)", "tuple")
	// `{name:}` is a struct; a block is `begin ... end`.
	wantKinds(t, "p = {name:}", "struct")
	wantKinds(t, "x = begin\n    y = 1\n    y\nend", "block")
	wantKinds(t, "l = Ids[1, 2]", "bracket Ids")
	wantKinds(t, "l = .Items[1, 2]", "bracket .Items")
	// Mixing the two in one brace is an error at the entry that changes.
	wantParseError(t, "m = {\"a\" => 1, b: 2}", "expected '=>' in map literal")
	wantParseError(t, "p = {a: 1, \"b\" => 2}", "expected field name in struct literal")
}

// A type built on a shape is named in front of the shape's brackets or
// braces, and the first entry in the braces says map or struct; `Name{}`
// is the struct literal node, which the checker reads as the empty map
// when Name is a type over a map.
func TestNamedShapeLiterals(t *testing.T) {
	wantKinds(t, "p = Point{x: 1}", "struct Point")
	wantKinds(t, "p = Point{}", "struct Point")
	wantKinds(t, "p = Point{x:}", "struct Point")
	wantKinds(t, "p = geo.Point{x: 1}", "struct geo.Point")
	wantKinds(t, "c = Shape.Circle{radius: 1.0}", "struct Shape.Circle")
	wantKinds(t, "c = geo.Shape.Circle{radius: 1.0}", "struct geo.Shape.Circle")
	wantKinds(t, "c: Shape = .Circle{radius: 1.0}", "struct .Circle")
	wantKinds(t, "c = Circle{radius: 1.0}", "struct Circle")
	// A named tuple is built by a call, `Pair(1, "x")`, which is no literal.
	wantKinds(t, "p = Pair(1, \"x\")", "")
	wantKinds(t, "v = Path[1, 2]", "bracket Path")
	wantKinds(t, "v: Bag = .Items[1, 2]", "bracket .Items")
	wantKinds(t, "k = Kvs{\"a\" => 4}", "map Kvs")
	wantKinds(t, "k = Kvs{}", "struct Kvs")
	wantKinds(t, "j: Json = .Obj{\"k\" => v}", "map .Obj")
	wantKinds(t, "j = Json.Obj{\"k\" => v}", "map Json.Obj")
	wantKinds(t, "b = shapes.Bag.Named{\"a\" => 4}", "map shapes.Bag.Named")
	wantKinds(t, "t = Tags[\"a\", \"b\"]", "bracket Tags")
	wantKinds(t, "p = Point{x: {1 => 2}}", "struct Point map")
	wantKinds(t, "p = Point{f: fn x -> case x\n    1 -> 2\n    _ -> 3\nend}", "struct Point block")
	wantParseError(t, "k = Kvs{..m, \"a\" => 1}", "map spread `..` has no named form; write `{..base, key => value}`")
	wantParseError(t, "p = Point{..q, x: 1}", "struct spread `..` has no named form; write `{..base, field: value}`")
	wantParseError(t, "v = Path[..v, 1]", "spread `..` has no named form; write `[..base, item]`")
}

// A name with a `{` on its line opens a literal in an `if` condition or a
// `case` subject as anywhere else.
func TestStructLiteralsInConditions(t *testing.T) {
	wantKinds(t, "x = if p == (Point{x: 1}) then 1 else 2", "struct Point block block")
	wantKinds(t, "x = case (Point{x: 1})\n    Point{x:} -> x\nend", "struct Point struct-pattern Point")
	wantKinds(t, "x = if s == .Paid then 1 else 2", "block block")
	wantKinds(t, "x = if c == {1 => 2} then 1 else 2", "map block block")
	wantKinds(t, "x = if c then 1 else 2", "block block")
	wantKinds(t, "x = if True then 1 else 2", "block block")
	// A literal first in the condition, with literals inside it, is the
	// comparison's operand, not a pattern.
	wantKinds(t, "x = if Point{x: 1} == p then 1 else 2", "struct Point block block")
	wantKinds(t, "x = if Kvs{\"a\" => 1} != m then 1 else 2", "map Kvs block block")
	wantKinds(t, "x = if (1, 2) == t then 1 else 2", "tuple block block")
}

func TestShapePatternsAndTypes(t *testing.T) {
	wantKinds(t, "x = case p\n    {x:, y:} -> x\n    _ -> 0\nend", "struct-pattern")
	wantKinds(t, "x = case p\n    {x: 0, y:} -> y\n    _ -> 0\nend", "struct-pattern")
	wantKinds(t, "{x:, y:} = p", "struct-destructure")
	wantKinds(t, "Point{x:, y:} = p", "struct-pattern Point")
	wantKinds(t, "fn f({x:, y:}: Point) -> Int\n    x\nend", "struct-pattern block")
	wantKinds(t, "fn f(Point{x:, y:}) -> Int\n    x\nend", "struct-pattern Point block")
	wantKinds(t, "fn f(p: {x: Int, y: List<{z: Int}>}) -> Int\n    p.x\nend", "struct-type struct-type block")
	wantKinds(t, "x = case m\n    {\"k\" => v} -> v\n    _ -> 0\nend", "map-pattern")
	wantKinds(t, "x = case m\n    {} -> 0\n    _ -> 1\nend", "map-pattern")
	wantKinds(t, "x = case m\n    Kvs{\"k\" => v} -> v\n    _ -> 0\nend", "map-pattern Kvs")
	wantKinds(t, "x = case j\n    .Obj{\"k\" => v} -> v\n    _ -> 0\nend", "map-pattern .Obj")
	wantKinds(t, "x = case p\n    Point{x:} -> x\n    _ -> 0\nend", "struct-pattern Point")
	wantKinds(t, "x = case s\n    .Circle{radius:} -> radius\n    Shape.Square{side:} -> side\nend", "struct-pattern .Circle struct-pattern Shape.Square")
	wantKinds(t, "x = case p\n    Pair(a, b) -> a\nend", "variant-pattern Pair tuple-pattern")
	wantKinds(t, "Pair(a, b) = p", "variant-pattern Pair tuple-pattern")
	wantKinds(t, "x = case p\n    Pair((a, b)) -> a\nend", "variant-pattern Pair tuple-pattern")
}

// A bracket pattern takes apart the list, vector or set its subject is:
// the parser reads one shape, heads, a `..` and tails, for all three.
func TestBracketPatterns(t *testing.T) {
	pattern := func(src string) *ast.ListPattern {
		t.Helper()
		var found *ast.ListPattern
		for _, n := range parse(t, src) {
			ast.Inspect(n, func(n ast.Node) bool {
				if v, ok := n.(*ast.ListPattern); ok && found == nil {
					found = v
				}
				return true
			})
		}
		if found == nil {
			t.Fatalf("Parse(%q): no bracket pattern", src)
		}
		return found
	}
	arms := func(pat string) string { return "x = case v\n    " + pat + " -> 0\n    _ -> 1\nend" }
	for _, c := range []struct {
		pat          string
		heads, tails int
		spread       bool
		rest         string
	}{
		{"[]", 0, 0, false, ""},
		{"[a, b]", 2, 0, false, ""},
		{"[first, ..rest]", 1, 0, true, "rest"},
		{"[.., last]", 0, 1, true, ""},
		{"[a, b, ..]", 2, 0, true, ""},
		{"[a, ..mid, z]", 1, 1, true, "mid"},
		{"[..all]", 0, 0, true, "all"},
		{"[\"admin\", ..rest]", 1, 0, true, "rest"},
	} {
		v := pattern(arms(c.pat))
		rest := ""
		if r, ok := v.Rest().(*ast.IdentPattern); ok {
			rest = r.Name
		}
		if len(v.Heads) != c.heads || len(v.Tails) != c.tails || (v.TailSpread != nil) != c.spread || rest != c.rest {
			t.Errorf("%s: heads %d tails %d spread %v rest %q", c.pat, len(v.Heads), len(v.Tails), v.TailSpread != nil, rest)
		}
		if c.spread && v.SpreadLine == 0 {
			t.Errorf("%s: no position for the `..`", c.pat)
		}
	}
	wantKinds(t, arms("[(a, b), ..rest]"), "bracket-pattern tuple-pattern")
	wantKinds(t, arms("[Some(x), ..]"), "bracket-pattern variant-pattern Some")
	wantKinds(t, arms("[a] or [a, _]"), "bracket-pattern bracket-pattern")
	wantKinds(t, arms("[a, ..] as whole"), "bracket-pattern")
	wantKinds(t, arms("Path[a, ..]"), "bracket-pattern Path")
	wantKinds(t, arms("Tags[\"a\", ..]"), "bracket-pattern Tags")
	wantKinds(t, "[first, ..rest] = v else\n    return 0\nend", "bracket-pattern block")
	wantKinds(t, "x = if [a, ..] = v then a else 0", "bracket-pattern block block")
	wantKinds(t, "f = fn [a, ..] -> a", "bracket-pattern block")
	wantParseError(t, arms("[a, .., b, ..]"), "a list pattern takes one `..`")
}

// The `#` and `%` that wrote a vector, a set or a map, or a type built on
// one, are errors that say to drop them, with the fix that does.
func TestSigilsAreErrors(t *testing.T) {
	for _, c := range []struct{ src, msg, fix string }{
		{"v = #[1, 2]", "a vector is written without `#`: `[1, 2]` where a Vector is expected, else `Vector[1, 2]`", "Vector"},
		{"s = %[1, 2]", "a set is written without `%`: `[1, 2]` where a Set is expected, else `Set[1, 2]`", "Set"},
		{"m = %{\"a\" => 1}", "a map is written without `%`: `{key => value}`", ""},
		{"p = #Path[1]", "a literal or pattern naming its type is written without `#`", ""},
		{"k = %Kvs{\"a\" => 1}", "a literal or pattern naming its type is written without `%`", ""},
		{"j: Json = %.Obj{\"a\" => 1}", "a literal or pattern naming its type is written without `%`", ""},
		{"x = case v\n    #[a, ..] -> a\nend", "a pattern is written without `#`", ""},
		{"x = case s\n    %[\"a\", ..] -> 1\nend", "a pattern is written without `%`", ""},
		{"x = case m\n    %{\"k\" => v} -> v\nend", "a map pattern is written without `%`", ""},
	} {
		_, err := Parse(lexer.Lex(c.src))
		var pe ParseError
		if !errors.As(err, &pe) || !strings.Contains(pe.Message, c.msg) {
			t.Errorf("Parse(%q) error = %v, want %q", c.src, err, c.msg)
			continue
		}
		if pe.Fix == nil || len(pe.Fix.Edits) != 1 || pe.Fix.Edits[0].Text != c.fix || pe.Fix.Edits[0].End.Col-pe.Fix.Edits[0].Start.Col != 1 {
			t.Errorf("Parse(%q) fix = %+v, want the sigil replaced by %q", c.src, pe.Fix, c.fix)
		}
	}
	// After an operand, `%` is the remainder.
	wantKinds(t, "x = a % b", "")
	wantKinds(t, "x = a % (b)", "")
	wantKinds(t, "x = a %[1]", "bracket")
}

// A type over an anonymous struct is written as a struct.
func TestShapeSymbolRefusals(t *testing.T) {
	wantParseError(t, "type Wrap {x: Int}", "a type over an anonymous struct is a struct: write `struct Wrap` with one field per line and `end`")
	wantParseError(t, "s = {1, 2}", "a `{` opens a struct")
}
