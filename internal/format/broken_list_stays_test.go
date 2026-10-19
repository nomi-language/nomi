package format

import "testing"

// A call or literal that breaks because it is too wide writes each element
// on its own line, and a struct literal in it across lines. The next format
// read that literal as written across lines by its author, which is
// LocalBroken and hidden from the list's fits check, so the list fit and
// hugged it (`same(Point{`, `[(` before a parenthesized element holding a
// comment, `Err(Shape{`). A list whose first element starts below its
// opening bracket and that holds an element broken whatever the width now
// stays one element per line.
func TestFormat_WidthBrokenListStaysBroken(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n" +
				"    m = same(#Point{zed_long_long_long_long_long_long_long_long: 0, alpha_long_long_long_long_long_long_long_long_long: 3}, #Point{zed: 0, alpha: 4})\n" +
				"}\n",
			"fn main() {\n" +
				"    m = same(\n" +
				"        #Point{\n" +
				"            zed_long_long_long_long_long_long_long_long: 0,\n" +
				"            alpha_long_long_long_long_long_long_long_long_long: 3,\n" +
				"        },\n" +
				"        #Point{zed: 0, alpha: 4},\n" +
				"    )\n" +
				"}\n",
		},
		{
			"fn main() {\n" +
				"    shapes_rather_long_rather_long_rather_long_rather_long: List<Shape> = [( // a33\n" +
				"        #Circle{radius: 5.0}), #Rectangle{width_rather_long_rather_long_rather_long_rather_long: 3.0, height_rather_long: 4.0}, Shape.Point]\n" +
				"}\n",
			"fn main() {\n" +
				"    shapes_rather_long_rather_long_rather_long_rather_long: List<Shape> = [\n" +
				"        (\n" +
				"            // a33\n" +
				"            #Circle{radius: 5.0}\n" +
				"        ),\n" +
				"        #Rectangle{\n" +
				"            width_rather_long_rather_long_rather_long_rather_long: 3.0,\n" +
				"            height_rather_long: 4.0,\n" +
				"        },\n" +
				"        Shape.Point,\n" +
				"    ]\n" +
				"}\n",
		},
		{
			// An arm too wide for its line puts every arm's body on the
			// line below its `->`; the next format found the literal
			// already broken, the arm fit, and the bodies joined their
			// arrows again.
			"fn decode(r: Result<Int, String>) -> Result<Int, Shape> {\n" +
				"    case r {\n" +
				"        Ok(parsed) -> Ok(parsed)\n" +
				"        Err(_) -> Err(#Shape{path: [], expected: \"json\", got_rather_long_rather_long_rather_long_rather_long: \"invalid\"})\n" +
				"    }\n" +
				"}\n",
			"fn decode(r: Result<Int, String>) -> Result<Int, Shape> {\n" +
				"    case r {\n" +
				"        Ok(parsed) ->\n" +
				"            Ok(parsed)\n" +
				"\n" +
				"        Err(_) ->\n" +
				"            Err(\n" +
				"                #Shape{\n" +
				"                    path: [],\n" +
				"                    expected: \"json\",\n" +
				"                    got_rather_long_rather_long_rather_long_rather_long: \"invalid\",\n" +
				"                }\n" +
				"            )\n" +
				"    }\n" +
				"}\n",
		},
		{
			// A literal its author stacked inside a call still hugs the
			// call's parenthesis.
			"fn main() {\n" +
				"    m = same(#Point{\n" +
				"        zed: 0,\n" +
				"        alpha: 3,\n" +
				"    }, #Point{zed: 0, alpha: 4})\n" +
				"}\n",
			"fn main() {\n" +
				"    m = same(#Point{\n" +
				"        zed: 0,\n" +
				"        alpha: 3,\n" +
				"    }, #Point{zed: 0, alpha: 4})\n" +
				"}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// The same holds for a struct literal's fields, a function's parameters,
// the short form of an `if` and a case arm's body below its `->`.
func TestFormat_WidthBrokenLayoutsStayBroken(t *testing.T) {
	src := "fn record_sum((p_rather_long_rather_long_rather_long): #{x_rather_long: Int, y_rather_long_rather_long_rather_long_rather_long: Int}) -> Int {\n" +
		"    p_rather_long_rather_long_rather_long.x\n" +
		"}\n" +
		"\n" +
		"fn label(n: Int) -> Check {\n" +
		"    if n > 0 { #Check.Valid{addr_rather_long_rather_long_rather_long_rather_long: \"a\", score_rather_long_rather_long_rather_long: 1} } else { Check.Invalid(\"x\") }\n" +
		"}\n" +
		"\n" +
		"fn label2(check: Check) -> String {\n" +
		"    #.Valid{addr} = check else {\n" +
		"        .Invalid(_) -> #{addr_rather_long_rather_long_rather_long_rather_long: \"unknown\", score_rather_long_rather_long_rather_long: 0}\n" +
		"    }\n" +
		"    addr\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"    p = #{address_rather_long: #Address{street_rather_long_rather_long_rather_long: \"1 Main\", city_rather_long: \"Springfield\"}}\n" +
		"}\n"
	want := "fn record_sum(\n" +
		"    p_rather_long_rather_long_rather_long: #{\n" +
		"        x_rather_long: Int,\n" +
		"        y_rather_long_rather_long_rather_long_rather_long: Int,\n" +
		"    },\n" +
		") -> Int {\n" +
		"    p_rather_long_rather_long_rather_long.x\n" +
		"}\n" +
		"\n" +
		"fn label(n: Int) -> Check {\n" +
		"    if n > 0 {\n" +
		"        #Check.Valid{\n" +
		"            addr_rather_long_rather_long_rather_long_rather_long: \"a\",\n" +
		"            score_rather_long_rather_long_rather_long: 1,\n" +
		"        }\n" +
		"    } else {\n" +
		"        Check.Invalid(\"x\")\n" +
		"    }\n" +
		"}\n" +
		"\n" +
		"fn label2(check: Check) -> String {\n" +
		"    #.Valid{addr} = check else {\n" +
		"        .Invalid(_) ->\n" +
		"            #{\n" +
		"                addr_rather_long_rather_long_rather_long_rather_long: \"unknown\",\n" +
		"                score_rather_long_rather_long_rather_long: 0,\n" +
		"            }\n" +
		"    }\n" +
		"\n" +
		"    addr\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"    p = #{\n" +
		"        address_rather_long: #Address{\n" +
		"            street_rather_long_rather_long_rather_long: \"1 Main\",\n" +
		"            city_rather_long: \"Springfield\",\n" +
		"        },\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}

// And for the short form of a pattern binding's `else` block.
func TestFormat_WidthBrokenElseBlockStaysBroken(t *testing.T) {
	src := "fn whole(c: Check) -> Int {\n" +
		"    .Valid(r_rather_long) = c else { #{addr_rather_long_rather_long_rather_long_rather_long: \"none\", score_rather_long_rather_long: -1} }\n" +
		"    r_rather_long.score\n" +
		"}\n"
	want := "fn whole(c: Check) -> Int {\n" +
		"    .Valid(r_rather_long) = c else {\n" +
		"        #{\n" +
		"            addr_rather_long_rather_long_rather_long_rather_long: \"none\",\n" +
		"            score_rather_long_rather_long: -1,\n" +
		"        }\n" +
		"    }\n" +
		"\n" +
		"    r_rather_long.score\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}

// And for a block of one statement, whose short form is `{ stmt }`. Found
// by FuzzFormatKeepsMeaning: the inner block forces the literal broken.
func TestFormat_BrokenBlockStaysBroken(t *testing.T) {
	src := "{#{a:\"\"a:{0;\"\"}}}\n"
	want := "{\n" +
		"    #{\n" +
		"        a: \"\",\n" +
		"        a: {\n" +
		"            0\n" +
		"            \"\"\n" +
		"        },\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}

// And for a map pattern whose key is a literal too wide for its line.
func TestFormat_WidthBrokenMapPatternStaysBroken(t *testing.T) {
	src := "fn main() {\n" +
		"    by_struct = case struct_keyed {\n" +
		"        %{#Point{x_rather_long_rather_long_rather_long_rather_long_rather_long: 1, y_rather_long_rather_long_rather_long: 2} => v} -> v\n" +
		"        _ -> \"?\"\n" +
		"    }\n" +
		"}\n"
	want := "fn main() {\n" +
		"    by_struct = case struct_keyed {\n" +
		"        %{\n" +
		"            #Point{\n" +
		"                x_rather_long_rather_long_rather_long_rather_long_rather_long: 1,\n" +
		"                y_rather_long_rather_long_rather_long: 2,\n" +
		"            } => v,\n" +
		"        } ->\n" +
		"            v\n" +
		"\n" +
		"        _ ->\n" +
		"            \"?\"\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
