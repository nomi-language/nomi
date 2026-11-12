package format

import (
	"strings"
	"testing"
)

// A body whose only statement is a block is written as the block's
// statements. Every case is checked for its text, for idempotence, for an AST
// equal to the source's with the same rewrite applied (checkTailReturnCase),
// and for keeping every comment.
func TestFormat_FlattensBodyThatIsOneBlock(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			"a tail return of a block",
			"fn f() -> Int\n    return begin\n        a = 1\n        a + 1\n    end\nend\n",
			"fn f() -> Int\n    a = 1\n    a + 1\nend\n",
		},
		{
			"a function body",
			"fn f() -> Int\n    begin\n        a = 1\n        a + 1\n    end\nend\n",
			"fn f() -> Int\n    a = 1\n    a + 1\nend\n",
		},
		{
			"nested blocks flatten all the way",
			"fn f() -> Int\n    begin\n        begin\n            c = 1\n            c\n        end\n    end\nend\n",
			"fn f() -> Int\n    c = 1\n    c\nend\n",
		},
		{
			"comments move to the body",
			"fn g() -> Int\n    // lead\n    begin\n        // inner\n        a = 1\n        a + 1\n        // tail inner\n    end\n    // tail outer\nend\n",
			"fn g() -> Int\n    // lead\n    // inner\n    a = 1\n    a + 1\n    // tail inner\n    // tail outer\nend\n",
		},
		{
			"if branches",
			"fn h(x: Int) -> Int\n    if x > 0 then\n        begin\n            a = 1\n            a\n        end\n    else\n        begin\n            b = 2\n            b\n        end\n    end\nend\n",
			"fn h(x: Int) -> Int\n    if x > 0 then\n        a = 1\n        a\n    else\n        b = 2\n        b\n    end\nend\n",
		},
		{
			"a braced case arm",
			"fn k(x: Int) -> Int\n    case x\n        0 ->\n            begin\n                b = 3\n                b\n            end\n        _ -> 1\n    end\nend\n",
			"fn k(x: Int) -> Int\n    case x\n        0 ->\n            b = 3\n            b\n\n        _ ->\n            1\n    end\nend\n",
		},
		{
			"a lambda body",
			"fn l(xs: List<Int>) -> List<Int>\n    xs\n    |> Iter.map(fn n ->\n        begin\n            m = n * 2\n            m + 1\n        end\n    end)\n    |> Iter.to_list()\nend\n",
			"fn l(xs: List<Int>) -> List<Int>\n    xs\n    |> Iter.map(fn n ->\n        m = n * 2\n        m + 1\n    end)\n    |> Iter.to_list()\nend\n",
		},
		{
			"a test body",
			"test \"t\"\n    begin\n        a = 1\n        assert a == 1\n    end\nend\n",
			"test \"t\"\n    a = 1\n    assert a == 1\nend\n",
		},
		{
			"a group's setup",
			"group \"g\"\n    setup begin\n        begin\n            x = 1\n            x\n        end\n    end\n\n    test \"t\", x\n        assert x == 1\n    end\nend\n",
			"group \"g\"\n    setup begin\n        x = 1\n        x\n    end\n\n    test \"t\", x\n        assert x == 1\n    end\nend\n",
		},
		{
			"a binding that shadows a parameter",
			"fn s(x: Int) -> Int\n    begin\n        x = x + 1\n        x\n    end\nend\n",
			"fn s(x: Int) -> Int\n    x = x + 1\n    x\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.want)
			checkKeepsComments(t, tc.src, tc.want)
		})
	}
}

// A block that is not the body's only statement, or whose statements would
// mean something else or could not run in the body, stays a block.
func TestFormat_KeepsBodyBlockItCannotFlatten(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"another statement before it", "fn a() -> Int\n    io.print(\"a\")\n\n    begin\n        b = 1\n        b\n    end\nend\n"},
		{"a comment after its brace", "fn m() -> Int\n    begin\n        c = 1\n        c\n    end // trailing\nend\n"},
		{"a defer", "fn d()\n    begin\n        defer io.print(\"bye\")\n        io.print(\"hi\")\n    end\nend\n"},
		{"a with", "fn w()\n    begin\n        with App.tag = \"x\"\n        run()\n    end\nend\n"},
		{"a nested fn", "fn n(x: Int) -> Int\n    begin\n        fn x2() -> Int\n            x * 2\n        end\n\n        x2()\n    end\nend\n"},
		{"an import", "fn i() -> Int\n    begin\n        import helper\n\n        helper.n()\n    end\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.src)
		})
	}
}

// An empty body stays empty.
func TestFormat_EmptyBodyIsNotABlockBody(t *testing.T) {
	src := "fn a() end\n"
	checkTailReturnCase(t, src, src)
}

func checkKeepsComments(t *testing.T, src, got string) {
	t.Helper()
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(got, line[i:]) {
			t.Errorf("comment %q lost:\n%s", line[i:], got)
		}
	}
}
