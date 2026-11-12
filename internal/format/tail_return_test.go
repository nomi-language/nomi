package format

import (
	"testing"
)

// A `return` that is the last thing a function or lambda body does is its
// value, so the formatter writes the value alone. Every case is checked for
// its text, for idempotence, and for an AST equal to the source's with the
// same returns stripped.
func TestFormat_StripsTailReturn(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			"function tail, comments kept",
			"fn a(x: Int) -> Int\n    y = x + 1\n    // the answer\n    return y // trailing\nend\n",
			"fn a(x: Int) -> Int\n    y = x + 1\n    // the answer\n    y // trailing\nend\n",
		},
		{
			"every branch of a tail if chain",
			"fn b(x: Int) -> Int\n    if x > 0 then\n        return 1\n    else if x < 0 then\n        return -1\n    else\n        return 0\n    end\nend\n",
			"fn b(x: Int) -> Int\n    if x > 0 then\n        1\n    else if x < 0 then\n        -1\n    else\n        0\n    end\nend\n",
		},
		{
			"case arms and a nested tail if",
			"fn c(x: Maybe<Int>) -> Int\n    case x\n        Some(n) ->\n            if n > 0 then\n                return n\n            else\n                y = n * 2\n                return y\n            end\n        None -> return 0\n    end\nend\n",
			"fn c(x: Maybe<Int>) -> Int\n    case x\n        Some(n) ->\n            if n > 0 then\n                n\n            else\n                y = n * 2\n                y\n            end\n\n        None ->\n            0\n    end\nend\n",
		},
		{
			"a case arm's comment stays on the arm",
			"fn d(x: Maybe<Int>) -> Int\n    case x\n        Some(n) -> return n // note\n        None -> 0\n    end\nend\n",
			"fn d(x: Maybe<Int>) -> Int\n    case x\n        Some(n) -> n // note\n        None -> 0\n    end\nend\n",
		},
		{
			"early return before the tail stays",
			"fn e(x: Int) -> Int\n    if x > 0 then\n        return 1\n    end\n    return 2\nend\n",
			"fn e(x: Int) -> Int\n    if x > 0 then return 1\n    2\nend\n",
		},
		{
			"lambda and callback tails; break and early return stay",
			"fn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn n ->\n        if n > 10 then break n\n        if n < 0 then return 0\n        return n * 3\n    end) |> Iter.to_list()\nend\n",
			"fn f(xs: List<Int>) -> List<Int>\n    xs\n    |> Iter.map(fn n ->\n        if n > 10 then break n\n        if n < 0 then return 0\n        n * 3\n    end)\n    |> Iter.to_list()\nend\n",
		},
		{
			"one-statement lambda",
			"fn g(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(fn n ->\n        return n + 1\n    end) |> Iter.to_list()\nend\n",
			"fn g(xs: List<Int>) -> List<Int>\n    xs\n    |> Iter.map(fn n ->\n        n + 1\n    end)\n    |> Iter.to_list()\nend\n",
		},
		{
			"then lambda keeps its block",
			"fn h(n: Int) -> Int\n    n\n    |> then fn v ->\n        return v * 2\n    end\nend\n",
			"fn h(n: Int) -> Int\n    n\n    |> then fn v ->\n        v * 2\n    end\nend\n",
		},
		{
			"returned lambda",
			"fn i() -> (Int) -> Int\n    return fn x -> x + 1\nend\n",
			"fn i() -> (Int) -> Int\n    fn x -> x + 1\nend\n",
		},
		{
			"impl function",
			"struct P\n    x: Int\nend\n\nimpl P\n    fn x(p: P) -> Int\n        return p.x\n    end\nend\n",
			"struct P\n    x: Int\nend\n\nimpl P\n    fn x(p: P) -> Int\n        p.x\n    end\nend\n",
		},
		{
			"bare return after a Unit statement goes, recursively",
			"fn j(x: Int)\n    if x > 0 then\n        io.print(\"pos\")\n        return\n    else\n        io.print(\"neg\")\n    end\n    return\nend\n",
			"fn j(x: Int)\n    if x > 0 then io.print(\"pos\") else io.print(\"neg\")\nend\n",
		},
		{
			"bare return in an if with no else",
			"fn k(x: Int)\n    if x > 0 then\n        io.print(\"pos\")\n        return\n    end\nend\n",
			"fn k(x: Int)\n    if x > 0 then io.print(\"pos\")\nend\n",
		},
		{
			"value opening with a brace",
			"fn i() -> {n: Int}\n    n = 1\n    m = n\n    return {n: m}\nend\n",
			"fn i() -> {n: Int}\n    n = 1\n    m = n\n    {n: m}\nend\n",
		},
		{
			"struct update after a binding",
			"fn i2(p: P) -> P\n    q = p\n    return {..q, x: q.x + 1}\nend\n",
			"fn i2(p: P) -> P\n    q = p\n    {..q, x: q.x + 1}\nend\n",
		},
		{
			"brace values in case arms and if branches",
			"fn j(x: Int) -> {n: Int}\n    case x\n        0 -> return {n: 0}\n        _ ->\n            if x > 0 then\n                return {n: x}\n            else\n                return {n: 1}\n            end\n    end\nend\n",
			"fn j(x: Int) -> {n: Int}\n    case x\n        0 -> {n: 0}\n        _ -> if x > 0 then {n: x} else {n: 1}\n    end\nend\n",
		},
		{
			"test body",
			"test \"t\"\n    assert 1 == 1\n    return\nend\n",
			"test \"t\"\n    assert 1 == 1\nend\n",
		},
		{
			"value return at a test body's end",
			"test \"t\"\n    assert 2 > 1\n    if 1 > 2 then return\n    return testing.check(1 == 1)\nend\n",
			"test \"t\"\n    assert 2 > 1\n    if 1 > 2 then return\n    testing.check(1 == 1)\nend\n",
		},
		{
			"bare return in a test body's tail if",
			"test \"t\"\n    assert 1 == 1\n    if 1 > 0 then\n        io.print(\"a\")\n        return\n    end\nend\n",
			"test \"t\"\n    assert 1 == 1\n    if 1 > 0 then io.print(\"a\")\nend\n",
		},
		{
			"test in a group",
			"group \"g\"\n    test \"t\"\n        assert 1 == 1\n        return\n    end\nend\n",
			"group \"g\"\n    test \"t\"\n        assert 1 == 1\n    end\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.want)
		})
	}
}

// What syntax alone cannot show is safe stays as written.
func TestFormat_KeepsTailReturnItCannotStrip(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"bare return alone in a body", "fn a()\n    return\nend\n"},
		{"bare return alone in a branch", "fn b(x: Int)\n    if x > 0 then return else io.print(\"x\")\nend\n"},
		{"bare return after a binding", "fn c()\n    _ = 1\n    return\nend\n"},
		{"bare return after dbg", "fn d()\n    dbg 1\n    return\nend\n"},
		{"bare return after a dbg stage", "fn e()\n    1 |> dbg\n\n    return\nend\n"},
		{"bare return with a comment", "fn f()\n    io.print(\"a\")\n    // done\n    return\nend\n"},
		{"bare return as a case arm", "fn h(x: Int)\n    case x\n        0 -> return\n        _ -> io.print(\"x\")\n    end\nend\n"},
		{"concurrent block", "fn k() -> Int\n    concurrent\n        t = Task.spawn(fn -> 1)\n        return Task.await(t)\n    end\nend\n"},
		{"value return in a test body's tail if", "test \"t\"\n    assert 1 == 1\n    if 1 > 0 then return 1 else return \"a\"\nend\n"},
		{"value return in a test body's tail case arm", "test \"t\"\n    assert 1 == 1\n\n    case 1\n        0 -> return 1\n        _ -> return \"a\"\n    end\nend\n"},
		{"group setup", "group \"g\"\n    setup begin\n        x = 1\n        return x\n    end\n\n    test \"t\", x\n        assert x == 1\n    end\nend\n"},
		{"binding else", "fn l(x: Maybe<Int>) -> Int\n    n = x else return 0\n    n\nend\n"},
		{"not the last statement of a tail if's block", "fn m(x: Int) -> Int\n    if x > 0 then return 1\n    x\nend\n"},
		// An `if` with no final `else` is a Maybe of its branches' value, so
		// `return None` there is not `None`.
		{"a branch of a tail else-if chain with no final else", "fn n(x: Int) -> Maybe<String>\n    if x < 0 then\n        return None\n    else if x == 0 then\n        \"zero\"\n    end\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.src)
		})
	}
}

func checkTailReturnCase(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, _ := Format(got); again != got {
		t.Errorf("not idempotent:\n%s\nthen:\n%s", got, again)
	}
	if err := SameMeaning(src, got); err != nil {
		t.Errorf("output's AST differs from the stripped source's: %v", err)
	}
}
