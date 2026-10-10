package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A comment in an argument list is the ItemComments of the argument it
// belongs to: one on its own line or after the `(` above the next argument,
// one ending a line after the argument's comma at the end of that argument,
// and one on its own line after the last argument below that argument.
func TestParse_CommentsInAnArgumentListBelongToTheirArguments(t *testing.T) {
	src := "x = add( // open\n    // first\n    1,\n    2, // second\n    // last\n)\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	call := nodes[0].(*ast.Binding).Value.(*ast.Call)
	texts := func(ts []ast.Trivia) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Text)
		}
		return out
	}
	one := call.Args[0].(*ast.IntLit).ItemComments
	two := call.Args[1].(*ast.IntLit).ItemComments
	if one == nil || len(one.Before) != 2 || one.Before[0].Text != "// open" || one.Before[1].Text != "// first" || len(one.Trailing) != 0 || len(one.After) != 0 {
		t.Errorf("1's comments = %+v, want // open and // first before it", one)
	}
	if two == nil || len(two.Before) != 0 || len(two.Trailing) != 1 || two.Trailing[0].Text != "// second" || len(two.After) != 1 || two.After[0].Text != "// last" {
		t.Errorf("2's comments: before %v, trailing %v, after %v; want // second trailing and // last after", texts(two.Before), texts(two.Trailing), texts(two.After))
	}
}

// A comment is accepted in each comma-separated list, where the parser
// used to stop at it.
func TestParse_AcceptsACommentInEveryList(t *testing.T) {
	for _, src := range []string{
		"x = f(\n    // c\n    1,\n)\n",
		"x = f(a: 1, // c\n    b: 2)\n",
		"x = [1, // c\n    2]\n",
		"x = #[1, // c\n    2]\n",
		"x = #{1, // c\n    2}\n",
		"x = {\"a\" => 1, // c\n    \"b\" => 2}\n",
		"x = (1, // c\n    2)\n",
		"f = |a, // c\n    b| a\n",
		"f = |\n    // c\n    a| a\n",
		"fn f(\n    // c\n    a: Int,\n) {}\n",
		"x: Map<\n    // c\n    String, Int> = {}\n",
		"fn f<\n    // c\n    A>(a: A): A { a }\n",
		"y = case x {\n    [a, // c\n        b] -> a\n    _ -> 0\n}\n",
		"y = case x {\n    (a, // c\n        b) -> a\n}\n",
		"y = case x {\n    P{a, // c\n        b} -> a\n}\n",
		"y = case x {\n    .Pair(a, // c\n        b) -> a\n}\n",
		"import std/comparable.{\n    // c\n    Comparable,\n}\n",
		"f: (Int, // c\n    Int) -> Int = add\n",
		"x = f<Int, // c\n    Int>(1)\n",
		"(a, // c\n    b) = pair\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}
