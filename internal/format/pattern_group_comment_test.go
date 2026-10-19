package format

import "testing"

// A comment inside parentheses that only group a pattern stays inside the
// brackets the pattern is an item of. It came back after the pattern, as a
// LineSuffix; on an arm that ends in a comment it took a line of its own
// below the arm, and after the last arm the next format read it as the
// case's end comment, so a lambda whose body is the case gained braces.
func TestFormat_PatternGroupCommentStaysInItsBrackets(t *testing.T) {
	src := "fn f(fragments: List<Fragment<String>>) -> String {\n" +
		"    Iter.reduce(fragments, fn acc = \"\", frag ->\n" +
		"        case frag {\n" +
		"            .Static(s) -> acc + s\n" +
		"            .Dynamic((\n" +
		"                // b5\n" +
		"                v)\n" +
		"            ) -> acc + v // t6\n" +
		"        }\n" +
		"    )\n" +
		"}\n"
	want := "fn f(fragments: List<Fragment<String>>) -> String {\n" +
		"    Iter.reduce(fragments, fn acc = \"\", frag -> {\n" +
		"        case frag {\n" +
		"            .Static(s) -> acc + s\n" +
		"            .Dynamic(\n" +
		"                // b5\n" +
		"                v\n" +
		"            ) -> acc + v // t6\n" +
		"        }\n" +
		"    })\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}

// The same holds for a tuple's and a list's elements, a spread, and a
// pattern that is not an item: an arm's, an `as` pattern's inner pattern
// and a field's, which keep the grouping parentheses around the comment.
func TestFormat_PatternGroupCommentsStayInParentheses(t *testing.T) {
	src := "fn f(p: #(Int, Int), xs: List<Int>, frag: Fragment<String>) -> Int {\n" +
		"    a = case p {\n" +
		"        #(x, (y // c1\n" +
		"        )) -> x // t1\n" +
		"        #(x, y) -> x + y\n" +
		"    }\n" +
		"    b = case xs {\n" +
		"        [(\n" +
		"            // c2\n" +
		"            h), ..rest] -> h // t2\n" +
		"        [(h // c3\n" +
		"        ), ..(rest // c4\n" +
		"        )] -> h // t5\n" +
		"        _ -> 0\n" +
		"    }\n" +
		"    c = case frag {\n" +
		"        .Dynamic((\n" +
		"            // c6\n" +
		"            v) as w) -> 1 // t6\n" +
		"        .Other(#Point{x: (\n" +
		"            // c7\n" +
		"            px)}) -> px // t7\n" +
		"    }\n" +
		"    d = case p {\n" +
		"        ((x // c8\n" +
		"        )) -> 0 // t8\n" +
		"    }\n" +
		"    0\n" +
		"}\n"
	want := "fn f(p: #(Int, Int), xs: List<Int>, frag: Fragment<String>) -> Int {\n" +
		"    a = case p {\n" +
		"        #(\n" +
		"            x,\n" +
		"            y\n" +
		"            // c1\n" +
		"        ) -> x // t1\n" +
		"        #(x, y) -> x + y\n" +
		"    }\n" +
		"\n" +
		"    b = case xs {\n" +
		"        [\n" +
		"            // c2\n" +
		"            h,\n" +
		"            ..rest\n" +
		"        ] -> h // t2\n" +
		"        [\n" +
		"            h,\n" +
		"            // c3\n" +
		"            ..rest\n" +
		"            // c4\n" +
		"        ] -> h // t5\n" +
		"        _ -> 0\n" +
		"    }\n" +
		"\n" +
		"    c = case frag {\n" +
		"        .Dynamic((\n" +
		"            // c6\n" +
		"            v\n" +
		"        ) as w) -> 1 // t6\n" +
		"        .Other(#Point{x: (\n" +
		"            // c7\n" +
		"            px\n" +
		"        )}) -> px // t7\n" +
		"    }\n" +
		"\n" +
		"    d = case p {\n" +
		"        (\n" +
		"            x\n" +
		"            // c8\n" +
		"        ) -> 0 // t8\n" +
		"    }\n" +
		"\n" +
		"    0\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
