package format

import "testing"

// A body whose one statement is a block flattenBodyBlocks leaves alone (an
// empty one, or one holding a `defer`) keeps the body's braces around it.
// Written bare after `setup` or a lambda's parameters, the inner block's
// braces read back as the body's, and the inner block is gone. Each case
// formats to its want, a second format leaves it unchanged, and the program
// means the same. The other block-bodied forms never wrote the inner block
// bare; their cases pin that.
func TestFormat_BodyThatIsOneBlockKeepsItsBraces(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"setup, empty block": {
			"group \"0\" { setup{{}}test \"\"{assert} }\n",
			"group \"0\" {\n    setup {\n        {}\n    }\n\n    test \"\" {\n        assert\n    }\n}\n",
		},
		"setup, nested empty blocks": {
			"group \"0\" { setup {{{}}} test \"\" { assert } }\n",
			"group \"0\" {\n    setup {\n        {}\n    }\n\n    test \"\" {\n        assert\n    }\n}\n",
		},
		"setup, block holding a defer": {
			"group \"0\" { setup { { defer f()\n 1 } } test \"\" { assert } }\n",
			"group \"0\" {\n    setup {\n        {\n            defer f()\n            1\n        }\n    }\n\n    test \"\" {\n        assert\n    }\n}\n",
		},
		"lambda, empty block": {
			"fn f() { x = fn -> {{}} }\n",
			"fn f() {\n    x = fn -> { {} }\n}\n",
		},
		"lambda with a parameter, empty block": {
			"fn f() { x = fn a -> {{}} }\n",
			"fn f() {\n    x = fn a -> { {} }\n}\n",
		},
		"tail lambda, empty block": {
			"fn f() { g(1, fn -> {{}}) }\n",
			"fn f() {\n    g(1, fn -> { {} })\n}\n",
		},
		"lambda, block holding a defer": {
			"fn f() { x = fn -> { { defer g()\n 1 } } }\n",
			"fn f() {\n    x = fn -> {\n        {\n            defer g()\n            1\n        }\n    }\n}\n",
		},
		"clock": {
			"group \"g\" { clock {{}} test \"\" { assert } }\n",
			"group \"g\" {\n    clock { {} }\n\n    test \"\" {\n        assert\n    }\n}\n",
		},
		"boot": {
			"group \"g\" { boot {{}} test \"\" { assert } }\n",
			"group \"g\" {\n    boot { {} }\n\n    test \"\" {\n        assert\n    }\n}\n",
		},
		"test body": {
			"test \"\" {{} assert}\n",
			"test \"\" {\n    {}\n    assert\n}\n",
		},
		"fn body": {
			"fn f() {{}}\n",
			"fn f() {\n    {}\n}\n",
		},
		"if branches": {
			"fn f() { y = if true {{}} else {{}} }\n",
			"fn f() {\n    y = if true { {} } else { {} }\n}\n",
		},
		"case arm": {
			"fn f(x: Int) { case x { _ -> {{}} } }\n",
			"fn f(x: Int) {\n    case x {\n        _ -> {\n            {}\n        }\n    }\n}\n",
		},
		"concurrent": {
			"fn f() { concurrent {{}} }\n",
			"fn f() {\n    concurrent {\n        {}\n    }\n}\n",
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
