package format

import "testing"

// Bodies are a header line, the statements below and `end`. Each case
// formats to its want, a second format leaves it unchanged (formatsTo), and
// the program means the same.
func TestFormat_EndBodies(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"empty bodies are one line",
			"fn noop()\nend\n\nstruct Empty\nend\n\nimpl Marker for Empty\nend\n\ninterface Marker\nend\n",
			"fn noop() end\n\nstruct Empty end\n\nimpl Marker for Empty end\n\ninterface Marker end\n",
		},
		{
			"a block if that is a binary operand goes in parentheses when inline",
			"fn f(c: Bool) -> Int\n    x = if c then\n        1\n    else\n        2\n    end + 3\n    x\nend\n",
			"fn f(c: Bool) -> Int\n    x = (if c then 1 else 2) + 3\n    x\nend\n",
		},
		{
			"an if at a pipe's head goes in parentheses when inline",
			"fn f(c: Bool) -> Int\n    if c then\n        1\n    else\n        2\n    end |> g()\nend\n",
			"fn f(c: Bool) -> Int\n    (if c then 1 else 2) |> g()\nend\n",
		},
		{
			"an if stage that a stage follows is in block form",
			"fn f(c: Bool) -> Int\n    c\n    |> if then\n        1\n    else\n        2\n    end\n    |> g()\nend\n",
			"fn f(c: Bool) -> Int\n    c\n    |> if then\n        1\n    else\n        2\n    end\n    |> g()\nend\n",
		},
		{
			"an else if chain shares one end",
			"fn f(c: Bool) -> Int\n    if c then 1 else if c then 2 else 3\nend\n",
			"fn f(c: Bool) -> Int\n    if c then\n        1\n    else if c then\n        2\n    else\n        3\n    end\nend\n",
		},
		{
			"an arm of several statements puts every arm's body below its ->",
			"fn f(x: Int) -> Int\n    case x\n        0 -> 1\n        _ ->\n            y = x + 1\n            y * 2\n    end\nend\n",
			"fn f(x: Int) -> Int\n    case x\n        0 ->\n            1\n\n        _ ->\n            y = x + 1\n            y * 2\n    end\nend\n",
		},
		{
			"a binding's else holds arms or a body, or its value inline",
			"fn f(m: Maybe<Int>, r: Result<Int, String>) -> Int\n    Some(n) = m else\n        return 0\n    end\n    Ok(k) = r else\n        Err(_) -> 1\n    end\n    n + k\nend\n",
			"fn f(m: Maybe<Int>, r: Result<Int, String>) -> Int\n    Some(n) = m else return 0\n\n    Ok(k) = r else\n        Err(_) -> 1\n    end\n\n    n + k\nend\n",
		},
		{
			"begin blocks",
			"fn f(n: Int) -> Int\n    x = begin\n        y = n\n        y + 1\n    end\n    z = begin end\n    x\nend\n",
			"fn f(n: Int) -> Int\n    x = begin\n        y = n\n        y + 1\n    end\n\n    z = begin end\n    x\nend\n",
		},
		{
			"comments after a header and before end",
			"fn f(x: Int) -> Int // header\n\n    y = x // after\n    // before end\n\nend\n",
			"fn f(x: Int) -> Int\n    // header\n    y = x // after\n    // before end\nend\n",
		},
		{
			"lambdas on one line, below their ->, and tail lambdas",
			"fn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn x -> x + 1) |> Iter.map(fn x ->\n        y = x\n        y\n    end) |> Iter.to_list()\nend\n",
			"fn f(xs: List<Int>) -> List<Int>\n    xs\n    |> Iter.map(fn x -> x + 1)\n    |> Iter.map(fn x ->\n        y = x\n        y\n    end)\n    |> Iter.to_list()\nend\n",
		},
		{
			"an import block",
			"import\n    std::io\n    std::lists.List\nend\n",
			"import\n    std::io\n    std::lists.List\nend\n",
		},
		{
			"tests, subtests and concurrent",
			"test \"t\"\n    subtests [1, 2], n\n        assert n > 0\n    end\nend\n\nfn f() -> Int\n    concurrent\n        1\n    end\nend\n",
			"test \"t\"\n    subtests [1, 2], n\n        assert n > 0\n    end\nend\n\nfn f() -> Int\n    concurrent\n        1\n    end\nend\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
