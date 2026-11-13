package format

import "testing"

// A binding `else` whose one statement holds an `if` or a `case` is in block
// form, so `else`, `return`, `if` and `then` never share a line; a plain
// `else return` stays on the binding's line, and an `if` inside a lambda in
// the statement does not count.
func TestFormat_BindingElseHoldingAConditionalIsABlock(t *testing.T) {
	src := "fn first(letters: List<Int>, total: Int, digits: Int) -> Maybe<Int>\n" +
		"    [letter, ..rest] = letters else return if total == 0 then Some(digits)\n" +
		"    Some(letter + List.length(rest))\n" +
		"end\n" +
		"\n" +
		"fn pick(m: Maybe<Int>, k: Int) -> Int\n" +
		"    Some(x) = m else return case k\n" +
		"        0 -> 1\n" +
		"        _ -> 2\n" +
		"    end\n" +
		"    x\n" +
		"end\n" +
		"\n" +
		"fn plain(m: Maybe<Int>, xs: List<Int>) -> Int\n" +
		"    Some(x) = m else return 0\n" +
		"    Some(y) = m else return xs |> Iter.count(fn n -> if n > 0 then True else False)\n" +
		"    x + y\n" +
		"end\n"
	want := "fn first(letters: List<Int>, total: Int, digits: Int) -> Maybe<Int>\n" +
		"    [letter, ..rest] = letters else\n" +
		"        return if total == 0 then Some(digits)\n" +
		"    end\n" +
		"    Some(letter + List.length(rest))\n" +
		"end\n" +
		"\n" +
		"fn pick(m: Maybe<Int>, k: Int) -> Int\n" +
		"    Some(x) = m else\n" +
		"        return case k\n" +
		"            0 -> 1\n" +
		"            _ -> 2\n" +
		"        end\n" +
		"    end\n" +
		"    x\n" +
		"end\n" +
		"\n" +
		"fn plain(m: Maybe<Int>, xs: List<Int>) -> Int\n" +
		"    Some(x) = m else return 0\n" +
		"    Some(y) = m else return xs |> Iter.count(fn n -> if n > 0 then True else False)\n" +
		"    x + y\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}
