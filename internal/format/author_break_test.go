package format

import "testing"

// A list, vector, set, map, tuple or struct literal, or a list, map or
// struct pattern, whose source breaks the line after its opening bracket
// stays broken one element per line even when it fits. Without that break
// the width and depth rules decide, so the same literal written on one line
// stays there. Each case is formatted twice (formatsKeepingMeaning).
func TestFormat_AuthorBreakAfterBracketKeepsALiteralBroken(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"list",
			"fn main() {\n    xs = [\n        \"a\", \"b\"]\n}\n",
			"fn main() {\n    xs = [\n        \"a\",\n        \"b\",\n    ]\n}\n",
		},
		{
			"list without the break",
			"fn main() {\n    xs = [\"a\",\n        \"b\",\n    ]\n}\n",
			"fn main() {\n    xs = [\"a\", \"b\"]\n}\n",
		},
		{
			"typed list",
			"fn main() {\n    xs = Names[\n        \"a\", \"b\"]\n}\n",
			"fn main() {\n    xs = Names[\n        \"a\",\n        \"b\",\n    ]\n}\n",
		},
		{
			"vector",
			"fn main() {\n    v = #[\n        \"a\", \"b\"]\n}\n",
			"fn main() {\n    v = #[\n        \"a\",\n        \"b\",\n    ]\n}\n",
		},
		{
			"vector without the break",
			"fn main() {\n    v = #[\"a\",\n        \"b\"]\n}\n",
			"fn main() {\n    v = #[\"a\", \"b\"]\n}\n",
		},
		{
			"vector with a spread first",
			"fn main() {\n    v = #[\n        ..w, \"b\"]\n}\n",
			"fn main() {\n    v = #[\n        ..w,\n        \"b\",\n    ]\n}\n",
		},
		{
			"set",
			"fn main() {\n    s = #{\n        \"a\", \"b\"}\n}\n",
			"fn main() {\n    s = #{\n        \"a\",\n        \"b\",\n    }\n}\n",
		},
		{
			"set without the break",
			"fn main() {\n    s = #{\"a\",\n        \"b\"}\n}\n",
			"fn main() {\n    s = #{\"a\", \"b\"}\n}\n",
		},
		{
			"map",
			"fn main() {\n    m = %{\n        \"a\" => 1, \"b\" => 2}\n}\n",
			"fn main() {\n    m = %{\n        \"a\" => 1,\n        \"b\" => 2,\n    }\n}\n",
		},
		{
			"map without the break",
			"fn main() {\n    m = %{\"a\" => 1,\n        \"b\" => 2}\n}\n",
			"fn main() {\n    m = %{\"a\" => 1, \"b\" => 2}\n}\n",
		},
		{
			"tuple",
			"fn main() {\n    t = #(\n        1, \"a\")\n}\n",
			"fn main() {\n    t = #(\n        1,\n        \"a\",\n    )\n}\n",
		},
		{
			"tuple without the break",
			"fn main() {\n    t = #(1,\n        \"a\")\n}\n",
			"fn main() {\n    t = #(1, \"a\")\n}\n",
		},
		{
			"struct",
			"fn main() {\n    p = Point{\n        x: 1, y: 2}\n}\n",
			"fn main() {\n    p = Point{\n        x: 1,\n        y: 2,\n    }\n}\n",
		},
		{
			// Only the break after the `{` counts: fields on several lines
			// with the first on the `{` line join.
			"struct without the break",
			"fn main() {\n    p = Point{x: 1,\n        y: 2}\n}\n",
			"fn main() {\n    p = Point{x: 1, y: 2}\n}\n",
		},
		{
			"struct with a spread first",
			"fn main() {\n    p = {\n        ..q, x: 1}\n}\n",
			"fn main() {\n    p = {\n        ..q,\n        x: 1,\n    }\n}\n",
		},
		{
			"list pattern",
			"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [\n            a, b] -> a + b\n        _ -> 0\n    }\n}\n",
			"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [\n            a,\n            b,\n        ] -> a + b\n        _ -> 0\n    }\n}\n",
		},
		{
			"list pattern without the break",
			"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [a,\n            b] -> a + b\n        _ -> 0\n    }\n}\n",
			"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [a, b] -> a + b\n        _ -> 0\n    }\n}\n",
		},
		{
			"map pattern",
			"fn f(m: Map<String, Int>) -> Int {\n    case m {\n        %{\n            \"a\" => a} -> a\n        _ -> 0\n    }\n}\n",
			"fn f(m: Map<String, Int>) -> Int {\n    case m {\n        %{\n            \"a\" => a,\n        } -> a\n        _ -> 0\n    }\n}\n",
		},
		{
			"struct pattern",
			"fn f(p: Point) -> Int {\n    case p {\n        Point{\n            x, y} -> x + y\n    }\n}\n",
			"fn f(p: Point) -> Int {\n    case p {\n        Point{\n            x,\n            y,\n        } -> x + y\n    }\n}\n",
		},
		{
			"struct pattern without the break",
			"fn f(p: Point) -> Int {\n    case p {\n        Point{x,\n            y} -> x + y\n    }\n}\n",
			"fn f(p: Point) -> Int {\n    case p {\n        Point{x, y} -> x + y\n    }\n}\n",
		},
		{
			// A kept-broken literal is local: the call around it hugs it,
			// as it hugs a struct literal written across lines.
			"hugged by a call",
			"fn main() {\n    f([\n        \"a\", \"b\"])\n}\n",
			"fn main() {\n    f([\n        \"a\",\n        \"b\",\n    ])\n}\n",
		},
		{
			// The elements of a kept-broken literal lay out on their own.
			"elements lay out by width",
			"fn main() {\n    xs = [\n        [1, 2], [3]]\n}\n",
			"fn main() {\n    xs = [\n        [1, 2],\n        [3],\n    ]\n}\n",
		},
		{
			// A comment after the bracket is the first element's, and the
			// break after it keeps the list broken anyway.
			"break followed by a comment",
			"fn main() {\n    xs = [\n        // first\n        \"a\", \"b\"]\n}\n",
			"fn main() {\n    xs = [\n        // first\n        \"a\",\n        \"b\",\n    ]\n}\n",
		},
		{
			"comment on the bracket's line",
			"fn main() {\n    xs = [ // why\n        \"a\", \"b\"]\n}\n",
			"fn main() {\n    xs = [\n        // why\n        \"a\",\n        \"b\",\n    ]\n}\n",
		},
		{
			"empty list",
			"fn main() {\n    xs: List<Int> = [\n    ]\n}\n",
			"fn main() {\n    xs: List<Int> = []\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// A board of string rows written one row per line keeps one row per line,
// even when it fits on one: a list of strings is never filled.
func TestFormat_BoardWrittenOneRowPerLineStaysSo(t *testing.T) {
	src := "test \"board\" {\n" +
		"    minefield = [\n" +
		"        \" *  * \",\n" +
		"        \"  *   \",\n" +
		"        \"    * \",\n" +
		"    ]\n" +
		"\n" +
		"    assert minesweeper.annotate(minefield) == [\n" +
		"        \"1*22*1\",\n" +
		"        \"12*322\",\n" +
		"        \" 123*2\",\n" +
		"    ]\n" +
		"}\n"
	formatsKeepingMeaning(t, src, src)
}

// A call's argument list, a parameter list and a type argument list break
// by width alone: a line break after the `(` or `<` does not keep them
// broken.
func TestFormat_AuthorBreakInArgumentsIsIgnored(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"call arguments",
			"fn main() {\n    x = f(\n        1, 2)\n}\n",
			"fn main() {\n    x = f(1, 2)\n}\n",
		},
		{
			"parameters",
			"fn f(\n    a: Int, b: Int) -> Int {\n    a + b\n}\n",
			"fn f(a: Int, b: Int) -> Int {\n    a + b\n}\n",
		},
		{
			"type arguments",
			"fn main() {\n    xs: Map<\n        String, Int> = Map.empty()\n}\n",
			"fn main() {\n    xs: Map<String, Int> = Map.empty()\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// An or-pattern broken for width, one alternative per line, holding a
// struct pattern broken for width: the next format finds the struct
// pattern broken after its `{` and keeps it so, and the alternatives stay
// one per line rather than hugging it.
func TestFormat_OrPatternAroundAKeptBrokenPatternStaysBroken(t *testing.T) {
	src := "fn describe(shape: Shape) -> String {\n" +
		"    case shape {\n" +
		"        .Circle{r_rather_long} or .Square{side_rather_long_rather_long_rather_long_rather_long_rather_long: r_rather_long} when r_rather_long > 10.0 -> \"big\"\n" +
		"        _ -> \"small\"\n" +
		"    }\n" +
		"}\n"
	got := formatTwice(t, src)
	if err := SameMeaning(src, got); err != nil {
		t.Fatal(err)
	}
}
