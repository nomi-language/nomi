package format

import "testing"

// A lambda body written as a braced block whose contents start on the line
// after the `{` stays a block with its contents on their own lines, even
// when it would fit on one line: the line break after the `{` is the
// author's. Each case is already formatted, so Format must return it
// unchanged.
func TestFormat_LambdaBlockBrokenAfterBraceStays(t *testing.T) {
	cases := map[string]string{
		"one expression": "fn f(xs: List<Int>): List<Int> {\n" +
			"    xs\n" +
			"    |> Iter.map(|x| {\n" +
			"        x + 1\n" +
			"    })\n" +
			"    |> Iter.to_list()\n" +
			"}\n",
		"an if": "fn f(n: Int): String {\n" +
			"    [(3, \"Fizz\")]\n" +
			"    |> Iter.reduce(|acc = \"\", (divisor, word)| {\n" +
			"        if n % divisor == 0 { acc + word } else { acc }\n" +
			"    })\n" +
			"}\n",
		"a case": "fn f(xs: List<Int>): List<Int> {\n" +
			"    xs\n" +
			"    |> Iter.filter(|x| {\n" +
			"        case x {\n" +
			"            0 -> false\n" +
			"            _ -> true\n" +
			"        }\n" +
			"    })\n" +
			"    |> Iter.to_list()\n" +
			"}\n",
		"nested lambdas": "fn f(xs: List<List<Int>>): List<Int> {\n" +
			"    xs\n" +
			"    |> Iter.map(|ys| {\n" +
			"        ys\n" +
			"        |> Iter.map(|y| {\n" +
			"            y + 1\n" +
			"        })\n" +
			"        |> Iter.count()\n" +
			"    })\n" +
			"    |> Iter.to_list()\n" +
			"}\n",
		"a named argument": "fn f(): Int {\n" +
			"    run(\n" +
			"        2,\n" +
			"        step: |x| {\n" +
			"            x + 1\n" +
			"        },\n" +
			"    )\n" +
			"}\n",
		"a pipe stage": "fn f(n: Int): Int {\n" +
			"    n\n" +
			"    |> then |x| {\n" +
			"        x * 2\n" +
			"    }\n" +
			"}\n",
		"bound to a name": "fn f() {\n" +
			"    g = |x: Int| {\n" +
			"        x + 1\n" +
			"    }\n" +
			"}\n",
		"a trailing comment inside": "fn f(xs: List<Int>): List<Int> {\n" +
			"    xs\n" +
			"    |> Iter.map(|x| {\n" +
			"        x * 2 // double\n" +
			"    })\n" +
			"    |> Iter.to_list()\n" +
			"}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got := formatTwice(t, src)
			if got != src {
				t.Fatalf("Format collapsed a lambda block broken after its `{`:\ngot:\n%s\nwant:\n%s", got, src)
			}
		})
	}
}

// A braced body written on the `{` line, or a bare body, is laid out as
// before: one expression that fits goes on the parameters' line without
// braces.
func TestFormat_LambdaBlockOnBraceLineCollapses(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"braced on one line": {
			src:  "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| { x + 1 }) |> Iter.to_list()\n}\n",
			want: "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| x + 1) |> Iter.to_list()\n}\n",
		},
		"braced, closing brace below": {
			src:  "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| { x + 1\n    }) |> Iter.to_list()\n}\n",
			want: "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| x + 1) |> Iter.to_list()\n}\n",
		},
		"bare body on the next line": {
			src:  "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x|\n        x + 1) |> Iter.to_list()\n}\n",
			want: "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| x + 1) |> Iter.to_list()\n}\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := formatTwice(t, c.src)
			if got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if err := SameMeaning(c.src, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Dropping the braces of `|x| { e }` and keeping them on a broken block
// are both the same program: the parser builds the same tree for either
// spelling, so SameMeaning holds between the two layouts in both
// directions.
func TestFormat_LambdaBlockBracesKeepMeaning(t *testing.T) {
	broken := "fn f(xs: List<Int>): List<Int> {\n" +
		"    xs\n" +
		"    |> Iter.map(|x| {\n" +
		"        x + 1\n" +
		"    })\n" +
		"    |> Iter.to_list()\n" +
		"}\n"
	flat := "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|x| x + 1) |> Iter.to_list()\n}\n"
	if err := SameMeaning(broken, flat); err != nil {
		t.Fatalf("braced block and bare body differ: %v", err)
	}
	if err := SameMeaning(flat, broken); err != nil {
		t.Fatalf("bare body and braced block differ: %v", err)
	}
}
