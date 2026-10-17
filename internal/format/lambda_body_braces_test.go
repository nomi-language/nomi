package format

import "testing"

// A lambda body that does not fit on its parameters' line goes below them
// as a braced block, as rustfmt writes a closure, wherever the lambda
// stands. A call the lambda ends closes `})`. Each case formats to its
// want, a second format leaves it unchanged (the braces the formatter
// added keep it a block, lambdaKeepsBlock), and the program means the same.
func TestFormat_LambdaBodyBelowParametersIsBraced(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"last call argument": {
			"fn f(line: Int) -> String {\n    text = Iter.reduce(divisors(), fn acc = \"\", #(divisor, word) -> if line % divisor == 0 { acc + word } else { acc })\n    text\n}\n",
			"fn f(line: Int) -> String {\n" +
				"    text = Iter.reduce(divisors(), fn acc = \"\", #(divisor, word) -> {\n" +
				"        if line % divisor == 0 { acc + word } else { acc }\n" +
				"    })\n" +
				"\n" +
				"    text\n" +
				"}\n",
		},
		"the old bare layout": {
			"fn f(line: Int) -> String {\n" +
				"    text = Iter.reduce(divisors(), fn acc = \"\", #(divisor, word) ->\n" +
				"        if line % divisor == 0 { acc + word } else { acc }\n" +
				"    )\n" +
				"    text\n" +
				"}\n",
			"fn f(line: Int) -> String {\n" +
				"    text = Iter.reduce(divisors(), fn acc = \"\", #(divisor, word) -> {\n" +
				"        if line % divisor == 0 { acc + word } else { acc }\n" +
				"    })\n" +
				"\n" +
				"    text\n" +
				"}\n",
		},
		"non-last call argument": {
			"fn f() {\n    h = run(fn a_long_parameter_name: Int, another_long_parameter: Int -> a_long_parameter_name + another_long_parameter, 3)\n}\n",
			"fn f() {\n" +
				"    h = run(\n" +
				"        fn a_long_parameter_name: Int, another_long_parameter: Int -> {\n" +
				"            a_long_parameter_name + another_long_parameter\n" +
				"        },\n" +
				"        3,\n" +
				"    )\n" +
				"}\n",
		},
		"named argument": {
			"fn f() {\n    k = run(2, step: fn a_long_parameter_name: Int, another_long_parameter: Int -> a_long_parameter_name + another_long_parameter)\n}\n",
			"fn f() {\n" +
				"    k = run(\n" +
				"        2,\n" +
				"        step: fn a_long_parameter_name: Int, another_long_parameter: Int -> {\n" +
				"            a_long_parameter_name + another_long_parameter\n" +
				"        },\n" +
				"    )\n" +
				"}\n",
		},
		"pipe stage call": {
			"fn f(xs: List<Int>) -> List<Int> {\n    xs |> Iter.map(fn a_long_parameter_name -> a_long_parameter_name * a_long_parameter_name + a_long_parameter_name + 100000) |> Iter.to_list()\n}\n",
			"fn f(xs: List<Int>) -> List<Int> {\n" +
				"    xs\n" +
				"    |> Iter.map(fn a_long_parameter_name -> {\n" +
				"        a_long_parameter_name * a_long_parameter_name + a_long_parameter_name + 100000\n" +
				"    })\n" +
				"    |> Iter.to_list()\n" +
				"}\n",
		},
		"lambda stage before another stage": {
			"x\n    // note\n    |> dbg fn -> y\n    |> f()\n",
			"x\n// note\n|> (fn -> {\n    y\n    |> f()\n})\n|> dbg\n",
		},
		"then stage": {
			"fn f(xs: List<Int>) -> Int {\n    xs |> then fn a_long_parameter_name -> Iter.count(a_long_parameter_name) + Iter.count(a_long_parameter_name) + 1\n}\n",
			"fn f(xs: List<Int>) -> Int {\n" +
				"    xs\n" +
				"    |> then fn a_long_parameter_name -> {\n" +
				"        Iter.count(a_long_parameter_name) + Iter.count(a_long_parameter_name) + 1\n" +
				"    }\n" +
				"}\n",
		},
		"tap stage": {
			"fn f(xs: List<Int>) -> List<Int> {\n    xs |> tap fn a_long_parameter_name -> io.print(\"counted ${Iter.count(a_long_parameter_name)} things, and that is all of them\")\n}\n",
			"fn f(xs: List<Int>) -> List<Int> {\n" +
				"    xs\n" +
				"    |> tap fn a_long_parameter_name -> {\n" +
				"        io.print(\"counted ${Iter.count(a_long_parameter_name)} things, and that is all of them\")\n" +
				"    }\n" +
				"}\n",
		},
		"bound to a name": {
			"fn f() {\n    g = fn a_long_parameter_name: Int, another_long_parameter: Int -> a_long_parameter_name + another_long_parameter + 1\n}\n",
			"fn f() {\n" +
				"    g = fn a_long_parameter_name: Int, another_long_parameter: Int -> {\n" +
				"        a_long_parameter_name + another_long_parameter + 1\n" +
				"    }\n" +
				"}\n",
		},
		"struct field": {
			"fn f() -> Seq {\n    Seq{run: fn yield: (Int) -> Bool -> if Iter.each_while(first_source, yield) { Iter.each_while(b, yield) } else { False }}\n}\n",
			"fn f() -> Seq {\n" +
				"    Seq{\n" +
				"        run: fn yield: (Int) -> Bool -> {\n" +
				"            if Iter.each_while(first_source, yield) {\n" +
				"                Iter.each_while(b, yield)\n" +
				"            } else {\n" +
				"                False\n" +
				"            }\n" +
				"        },\n" +
				"    }\n" +
				"}\n",
		},
		"list item": {
			"fn f() {\n    xs = [fn a_long_parameter_name: Int, another_long_parameter: Int -> a_long_parameter_name + another_long_parameter + 77]\n}\n",
			"fn f() {\n" +
				"    xs = [\n" +
				"        fn a_long_parameter_name: Int, another_long_parameter: Int -> {\n" +
				"            a_long_parameter_name + another_long_parameter + 77\n" +
				"        },\n" +
				"    ]\n" +
				"}\n",
		},
		"nested lambdas": {
			"fn f(xs: List<List<Int>>) -> List<Int> {\n    xs |> Iter.map(fn ys -> Iter.reduce(ys, fn a_long_accumulator_name = 0, y -> a_long_accumulator_name + y * y + a_long_accumulator_name + 1000000)) |> Iter.to_list()\n}\n",
			"fn f(xs: List<List<Int>>) -> List<Int> {\n" +
				"    xs\n" +
				"    |> Iter.map(fn ys -> {\n" +
				"        Iter.reduce(ys, fn a_long_accumulator_name = 0, y -> {\n" +
				"            a_long_accumulator_name + y * y + a_long_accumulator_name + 1000000\n" +
				"        })\n" +
				"    })\n" +
				"    |> Iter.to_list()\n" +
				"}\n",
		},
		"a case body": {
			"fn f(frags: List<Int>) -> Int {\n    Iter.reduce(frags, fn acc = 0, frag -> case frag {\n0 -> acc\n_ -> acc + 1\n})\n}\n",
			"fn f(frags: List<Int>) -> Int {\n" +
				"    Iter.reduce(frags, fn acc = 0, frag -> {\n" +
				"        case frag {\n" +
				"            0 -> acc\n" +
				"            _ -> acc + 1\n" +
				"        }\n" +
				"    })\n" +
				"}\n",
		},
		"an if that breaks the loop": {
			"fn f() -> Int {\n    Iter.loop(fn n = 0 -> if n >= 3 { break n } else { n + 1 })\n}\n",
			"fn f() -> Int {\n" +
				"    Iter.loop(fn n = 0 -> {\n" +
				"        if n >= 3 {\n" +
				"            break n\n" +
				"        } else {\n" +
				"            n + 1\n" +
				"        }\n" +
				"    })\n" +
				"}\n",
		},
		"a comment inside the body": {
			"fn f(xs: List<Int>) -> List<Int> {\n" +
				"    xs |> Iter.map(fn x -> if x > 0 {\n" +
				"        // keep positives\n" +
				"        x\n" +
				"    } else { 0 }) |> Iter.to_list()\n" +
				"}\n",
			"fn f(xs: List<Int>) -> List<Int> {\n" +
				"    xs\n" +
				"    |> Iter.map(fn x -> {\n" +
				"        if x > 0 {\n" +
				"            // keep positives\n" +
				"            x\n" +
				"        } else {\n" +
				"            0\n" +
				"        }\n" +
				"    })\n" +
				"    |> Iter.to_list()\n" +
				"}\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := formatTwice(t, c.src)
			if got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if err := SameMeaning(c.src, got); err != nil {
				t.Fatalf("%v\nformatted:\n%s", err, got)
			}
		})
	}
}

// A body that fits on the parameters' line stays there without braces, in
// every position the braced layout applies to.
func TestFormat_LambdaBodyThatFitsStaysBare(t *testing.T) {
	for _, src := range []string{
		"fn f() {\n    g = fn x -> x + 1\n}\n",
		"fn f(xs: List<Int>) -> List<Int> {\n    xs |> Iter.map(fn x -> x + 1) |> Iter.to_list()\n}\n",
		"fn f(xs: List<Int>) -> Int {\n    Iter.reduce(xs, fn acc = 0, x -> if x > 0 { acc + x } else { acc })\n}\n",
		"fn f() {\n    k = run(fn x -> x + 1, 3, step: fn x -> x * 2)\n}\n",
		"fn f(xs: List<Int>) -> Int {\n    xs\n    |> then fn v -> Iter.count(v)\n}\n",
		"fn f() {\n    xs = [fn x -> x + 1, fn x -> x * 2]\n}\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
	}
}

// Braces the formatter added stay when the body later fits: read back, the
// block starts below its `{`, which the formatter keeps as written.
func TestFormat_AddedLambdaBracesStayWhenTheBodyShrinks(t *testing.T) {
	src := "fn f(xs: List<Int>) -> List<Int> {\n" +
		"    xs\n" +
		"    |> Iter.map(fn x -> {\n" +
		"        x + 1\n" +
		"    })\n" +
		"    |> Iter.to_list()\n" +
		"}\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}
