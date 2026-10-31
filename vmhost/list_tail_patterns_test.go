package vmhost_test

import "testing"

// A list pattern with elements after its `..` reads them from the list's
// end: heads from the front, tails from the back, and a named rest the List
// of the elements between them. Lists of 0, 1, 2 and several elements each
// select the arm their length and ends match, in a case and in a binding's
// else.
func TestListPatternWithTails_Runs(t *testing.T) {
	got := runOutput(t, `import std/io

fn shape(xs: List<Int>) -> String {
    case xs {
        [] -> "empty"
        [only] -> "one ${only}"
        [first, ..mid, last] -> "first ${first}, mid ${mid}, last ${last}"
    }
}

fn ends(xs: List<Int>) -> String {
    case xs {
        [1, .., 9] -> "one to nine"
        [.., 9] -> "ends in nine"
        [a, b, .., y, z] -> "${a} ${b} .. ${y} ${z}"
        _ -> "other"
    }
}

fn last_two(xs: List<Int>) -> String {
    [.., a, b] = xs else {
        return "short"
    }
    "${a} ${b}"
}

fn body(lines: List<String>) -> String {
    ["graph {", ..inner, "}"] = lines else {
        return "not a graph"
    }
    String.join(inner, "|")
}

fn pairs(xs: List<#(String, Int)>) -> String {
    case xs {
        [#(k, _), .., #(_, v)] -> "${k} ${v}"
        _ -> "none"
    }
}

fn main() {
    Iter.each([[], [1], [1, 2], [1, 2, 3, 4, 5]], fn xs -> {
        io.print(shape(xs))
    })
    Iter.each([[], [9], [1, 9], [1, 5, 9], [2, 9], [1, 2, 3, 4], [1, 2, 3]], fn xs -> {
        io.print(ends(xs))
    })
    Iter.each([[], [1], [1, 2], [1, 2, 3, 4]], fn xs -> {
        io.print(last_two(xs))
    })
    io.print(body(["graph {", "a -- b", "b -- c", "}"]))
    io.print(body(["graph {", "}"]))
    io.print(body(["graph {"]))
    io.print(body(["graph {", "a"]))
    io.print(pairs([#("a", 1), #("b", 2), #("c", 3)]))
    io.print(pairs([#("a", 1)]))
    io.print(pairs([]))
}
`)
	want := "empty\none 1\nfirst 1, mid [], last 2\nfirst 1, mid [2, 3, 4], last 5\n" +
		"other\nends in nine\none to nine\none to nine\nends in nine\n1 2 .. 3 4\nother\n" +
		"short\nshort\n1 2\n3 4\n" +
		"a -- b|b -- c\n\nnot a graph\nnot a graph\n" +
		"a 3\nnone\nnone\n"
	if got != want {
		t.Errorf("output:\n got %q\nwant %q", got, want)
	}
}
