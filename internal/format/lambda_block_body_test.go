package format

import "testing"

// A lambda body written as a braced block whose contents start on the line
// after the `{` stays a block with its contents on their own lines, even
// when it would fit on one line: the line break after the `{` is the
// author's. Each case is already formatted, so Format must return it
// unchanged.
func TestFormat_LambdaBlockBrokenAfterBraceStays(t *testing.T) {
	cases := map[string]string{
		"one expression": "fn f(xs: List<Int>) -> List<Int>\n" +
			"    xs\n" +
			"    |> Iter.map(fn x ->\n" +
			"        x + 1\n" +
			"    end)\n" +
			"    |> Iter.to_list()\n" +
			"end\n",
		"an if": "fn f(n: Int) -> String\n" +
			"    [(3, \"Fizz\")]\n" +
			"    |> Iter.reduce(\"\", fn acc, (divisor, word) ->\n" +
			"        if n % divisor == 0 then acc + word else acc\n" +
			"    end)\n" +
			"end\n",
		"a case": "fn f(xs: List<Int>) -> List<Int>\n" +
			"    xs\n" +
			"    |> Iter.filter(fn x ->\n" +
			"        case x\n" +
			"            0 -> false\n" +
			"            _ -> true\n" +
			"        end\n" +
			"    end)\n" +
			"    |> Iter.to_list()\n" +
			"end\n",
		"nested lambdas": "fn f(xs: List<List<Int>>) -> List<Int>\n" +
			"    xs\n" +
			"    |> Iter.map(fn ys ->\n" +
			"        ys\n" +
			"        |> Iter.map(fn y ->\n" +
			"            y + 1\n" +
			"        end)\n" +
			"        |> Iter.count()\n" +
			"    end)\n" +
			"    |> Iter.to_list()\n" +
			"end\n",
		"a named argument": "fn f() -> Int\n" +
			"    run(\n" +
			"        2,\n" +
			"        step: fn x ->\n" +
			"            x + 1\n" +
			"        end,\n" +
			"    )\n" +
			"end\n",
		"a pipe stage": "fn f(n: Int) -> Int\n" +
			"    n\n" +
			"    |> then fn x ->\n" +
			"        x * 2\n" +
			"    end\n" +
			"end\n",
		"bound to a name": "fn f()\n" +
			"    g = fn x: Int ->\n" +
			"        x + 1\n" +
			"    end\n" +
			"end\n",
		"a trailing comment inside": "fn f(xs: List<Int>) -> List<Int>\n" +
			"    xs\n" +
			"    |> Iter.map(fn x ->\n" +
			"        x * 2 // double\n" +
			"    end)\n" +
			"    |> Iter.to_list()\n" +
			"end\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got := formatTwice(t, src)
			if got != src {
				t.Fatalf("Format collapsed a lambda block written below its `->`:\ngot:\n%s\nwant:\n%s", got, src)
			}
		})
	}
}

// A one-line body stays on the parameters' line when it fits.
func TestFormat_LambdaOneLineBodyStays(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"braced on one line": {
			src:  "fn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn x -> x + 1) |> Iter.to_list()\nend\n",
			want: "fn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn x -> x + 1) |> Iter.to_list()\nend\n",
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

// A one-line body and the same body below `->`, closed by `end`, are the
// same program, so SameMeaning holds between the two layouts in both
// directions.
func TestFormat_LambdaBlockBracesKeepMeaning(t *testing.T) {
	broken := "fn f(xs: List<Int>) -> List<Int>\n" +
		"    xs\n" +
		"    |> Iter.map(fn x ->\n" +
		"        x + 1\n" +
		"    end)\n" +
		"    |> Iter.to_list()\n" +
		"end\n"
	flat := "fn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn x -> x + 1) |> Iter.to_list()\nend\n"
	if err := SameMeaning(broken, flat); err != nil {
		t.Fatalf("braced block and bare body differ: %v", err)
	}
	if err := SameMeaning(flat, broken); err != nil {
		t.Fatalf("bare body and braced block differ: %v", err)
	}
}
