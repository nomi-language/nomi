package parser

import (
	"strings"
	"testing"
)

// No comma-separated list leaves an empty slot: a second comma, on the same
// line or after a line break or a comment, is an error at it.
func TestDoubledComma_IsRejected(t *testing.T) {
	const msg = "unexpected `,`: there is nothing between these commas"
	for _, tc := range []struct{ name, src, at string }{
		{"struct literal", "q = #Point{x: 1,, y: 2}\n", "line 1, col 17"},
		{"variant struct literal", "q = #.Circle{radius: 1.0,, x: 2}\n", "line 1, col 26"},
		{"anonymous struct literal", "q = #{x: 1,, y: 2}\n", "line 1, col 12"},
		{"anonymous struct literal, trailing", "q = #{x: 1,,}\n", "line 1, col 12"},
		{"struct literal across lines", "q = #{\n    x: 1,\n    ,\n    y: 2,\n}\n", "line 3, col 5"},
		{"struct literal after a comment", "q = #{x: 1, // c\n, y: 2}\n", "line 2, col 1"},
		{"update after its spread", "q = #{..p,, x: 1}\n", "line 1, col 11"},
		{"update after its spread across lines", "q = #{..p,\n,\n x: 1}\n", "line 2, col 1"},
		{"bare update after its spread", "q = #{.. ,, x: 1}\n", "line 1, col 11"},
		{"update between fields", "q = #{..p, x: 1,, y: 2}\n", "line 1, col 17"},
		{"map literal", "q = %{\"a\" => 1,, \"b\" => 2}\n", "line 1, col 16"},
		{"named map literal", "q = %Kvs{\"a\" => 1,, \"b\" => 2}\n", "line 1, col 19"},
		{"map update after its spread", "q = %{..m,, \"b\" => 2}\n", "line 1, col 11"},
		{"set", "q = %[1,, 2]\n", "line 1, col 9"},
		{"list", "q = [1,, 2]\n", "line 1, col 8"},
		{"list, trailing", "q = [1,,]\n", "line 1, col 8"},
		{"named list", "q = Ids[1,, 2]\n", "line 1, col 11"},
		{"vector", "q = #[1,, 2]\n", "line 1, col 9"},
		{"tuple", "q = #(1,, 2)\n", "line 1, col 9"},
		{"named tuple", "q = #Pair(1,, 2)\n", "line 1, col 13"},
		{"call arguments", "q = f(1,, 2)\n", "line 1, col 9"},
		{"call arguments across lines", "q = f(1,\n,\n 2)\n", "line 2, col 1"},
		{"named call arguments", "q = f(a: 1,, b: 2)\n", "line 1, col 12"},
		{"owner call arguments", "q = Foo.bar(1,, 2)\n", "line 1, col 15"},
		{"parameters", "fn f(a: Int,, b: Int) -> Int { a }\n", "line 1, col 13"},
		{"host fn parameters", "host fn f(a: Int,, b: Int) -> Int\n", "line 1, col 18"},
		{"interface function parameters", "interface I {\n    fn f(a: self,, b: Int) -> Int\n}\n", "line 2, col 18"},
		{"lambda parameters", "q = fn a,, b -> a\n", "line 1, col 10"},
		{"struct pattern", "x = case p {\n    #Point{x,, y} -> 0\n}\n", "line 2, col 14"},
		{"struct pattern with values", "x = case p {\n    #Point{x: 1,, y: 2} -> 0\n}\n", "line 2, col 17"},
		{"anonymous struct pattern", "x = case p {\n    #{x,, y} -> 0\n}\n", "line 2, col 9"},
		{"struct destructure", "#{a,, b} = p\n", "line 1, col 5"},
		{"tuple pattern", "x = case p {\n    #(a,, b) -> 0\n}\n", "line 2, col 9"},
		{"tuple destructure", "#(a,, b) = p\n", "line 1, col 5"},
		{"vector pattern", "x = case p {\n    #[a,, b] -> 0\n}\n", "line 2, col 9"},
		{"list pattern", "x = case p {\n    [a,, b] -> 0\n}\n", "line 2, col 8"},
		{"set pattern", "x = case p {\n    %[a,, b] -> 0\n}\n", "line 2, col 9"},
		{"map pattern", "x = case p {\n    %{\"a\" => a,, \"b\" => b} -> 0\n}\n", "line 2, col 16"},
		{"variant pattern", "x = case p {\n    Some(a,, b) -> 0\n}\n", "line 2, col 12"},
		{"dot variant pattern", "x = case p {\n    .A(a,, b) -> 0\n}\n", "line 2, col 10"},
		{"test setup pattern", "test \"x\", #(a,, b) {\n}\n", "line 1, col 15"},
		{"type arguments", "x: Map<String,, Int> = m\n", "line 1, col 15"},
		{"tuple type", "x: #(Int,, Int) = p\n", "line 1, col 10"},
		{"function type", "f: (Int,, Int) -> Int = g\n", "line 1, col 9"},
		{"impl type arguments", "impl Foo<A,, B> for X {}\n", "line 1, col 12"},
		{"function type parameters", "fn f<A,, B>(a: A) -> A { a }\n", "line 1, col 8"},
		{"struct type parameters", "struct S<A,, B> {\n    a: A\n}\n", "line 1, col 12"},
		{"where clause", "fn f(a: Int) -> Int where A: B,, C: D { a }\n", "line 1, col 32"},
		{"variant payload", "enum E {\n    A(Int,, Int)\n}\n", "line 2, col 11"},
		{"import list", "import a.{b,, c}\n", "line 1, col 13"},
		{"interpolation", "q = \"${f(1,, 2)}\"\n", "line 1, col 12"},
	} {
		err := parseError(t, tc.src)
		if want := tc.at + ": " + msg; !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %q, want %q", tc.name, err, want)
		}
	}
}

// One comma between items still parses, and so does a trailing comma in
// the lists that take one (literals, calls, parameters, struct and map
// patterns, import lists).
func TestDoubledComma_SingleAndTrailingCommasParse(t *testing.T) {
	for _, src := range []string{
		"q = #Point{x: 1, y: 2,}\n",
		"q = #{x: 1, y: 2,}\n",
		"q = #{\n    x: 1,\n    y: 2,\n}\n",
		"q = #{x: 1, // c\n    y: 2}\n",
		"q = #{..p, x: 1, y: 2,}\n",
		"q = #{\n    ..p,\n    x: 1,\n}\n",
		"q = #{a: #{.., x: 1,}}\n",
		"q = %{\"a\" => 1, \"b\" => 2,}\n",
		"q = %{..m, \"b\" => 2,}\n",
		"q = %[1, 2,]\n",
		"q = [1, 2,]\n",
		"q = Ids[1, 2,]\n",
		"q = #[1, 2,]\n",
		"q = #(1, 2,)\n",
		"q = f(1, 2,)\n",
		"q = f(\n    a: 1,\n    b: 2,\n)\n",
		"fn f(a: Int, b: Int,) -> Int { a }\n",
		"q = fn a, b -> a\n",
		"x = case p {\n    #Point{x, y,} -> 0\n}\n",
		"#{a, b,} = p\n",
		"x = case p {\n    [a, b,] -> 0\n}\n",
		"x = case p {\n    %{\"a\" => a, \"b\" => b,} -> 0\n}\n",
		"import a.{b, c,}\n",
	} {
		parse(t, src)
	}
}
