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
			"fn f(): Int {\n    return {\n        a = 1\n        a + 1\n    }\n}\n",
			"fn f(): Int {\n    a = 1\n    a + 1\n}\n",
		},
		{
			"a function body",
			"fn f(): Int {\n    {\n        a = 1\n        a + 1\n    }\n}\n",
			"fn f(): Int {\n    a = 1\n    a + 1\n}\n",
		},
		{
			"nested blocks flatten all the way",
			"fn f(): Int {\n    {\n        {\n            c = 1\n            c\n        }\n    }\n}\n",
			"fn f(): Int {\n    c = 1\n    c\n}\n",
		},
		{
			"comments move to the body",
			"fn g(): Int {\n    // lead\n    {\n        // inner\n        a = 1\n        a + 1\n        // tail inner\n    }\n    // tail outer\n}\n",
			"fn g(): Int {\n    // lead\n    // inner\n    a = 1\n    a + 1\n    // tail inner\n    // tail outer\n}\n",
		},
		{
			"if branches",
			"fn h(x: Int): Int {\n    if x > 0 {\n        {\n            a = 1\n            a\n        }\n    } else {\n        {\n            b = 2\n            b\n        }\n    }\n}\n",
			"fn h(x: Int): Int {\n    if x > 0 {\n        a = 1\n        a\n    } else {\n        b = 2\n        b\n    }\n}\n",
		},
		{
			"a braced case arm",
			"fn k(x: Int): Int {\n    case x {\n        0 -> {\n            {\n                b = 3\n                b\n            }\n        }\n        _ -> 1\n    }\n}\n",
			"fn k(x: Int): Int {\n    case x {\n        0 -> {\n            b = 3\n            b\n        }\n\n        _ ->\n            1\n    }\n}\n",
		},
		{
			"a lambda body",
			"fn l(xs: List<Int>): List<Int> {\n    xs\n    |> Iter.map(|n| {\n        {\n            m = n * 2\n            m + 1\n        }\n    })\n    |> Iter.to_list()\n}\n",
			"fn l(xs: List<Int>): List<Int> {\n    xs\n    |> Iter.map(|n| {\n        m = n * 2\n        m + 1\n    })\n    |> Iter.to_list()\n}\n",
		},
		{
			"a test body",
			"test \"t\" {\n    {\n        a = 1\n        assert a == 1\n    }\n}\n",
			"test \"t\" {\n    a = 1\n    assert a == 1\n}\n",
		},
		{
			"a group's setup",
			"tests \"g\" {\n    setup {\n        {\n            x = 1\n            x\n        }\n    }\n\n    test \"t\", x {\n        assert x == 1\n    }\n}\n",
			"tests \"g\" {\n    setup {\n        x = 1\n        x\n    }\n\n    test \"t\", x {\n        assert x == 1\n    }\n}\n",
		},
		{
			"a binding that shadows a parameter",
			"fn s(x: Int): Int {\n    {\n        x = x + 1\n        x\n    }\n}\n",
			"fn s(x: Int): Int {\n    x = x + 1\n    x\n}\n",
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
		{"another statement before it", "fn a(): Int {\n    io.print(\"a\")\n\n    {\n        b = 1\n        b\n    }\n}\n"},
		{"a comment after its brace", "fn m(): Int {\n    {\n        c = 1\n        c\n    } // trailing\n}\n"},
		{"a defer", "fn d() {\n    {\n        defer io.print(\"bye\")\n        io.print(\"hi\")\n    }\n}\n"},
		{"a with", "fn w() {\n    {\n        with App.tag = \"x\"\n        run()\n    }\n}\n"},
		{"a nested fn", "fn n(x: Int): Int {\n    {\n        fn x2(): Int {\n            x * 2\n        }\n\n        x2()\n    }\n}\n"},
		{"an import", "fn i(): Int {\n    {\n        import helper\n\n        helper.n()\n    }\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.src)
		})
	}
}

// An empty body stays empty.
func TestFormat_EmptyBodyIsNotABlockBody(t *testing.T) {
	src := "fn a() {}\n"
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
