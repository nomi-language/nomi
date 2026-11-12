package format

import "testing"

// A body whose one statement is a block flattenBodyBlocks leaves alone (an
// empty one, or one holding a `defer`) keeps it as a `begin ... end` inside
// the body. Written as the body itself, the inner block would be gone. Each case
// formats to its want, a second format leaves it unchanged, and the program
// means the same. The other block-bodied forms never wrote the inner block
// bare; their cases pin that.
func TestFormat_BodyThatIsOneBlockKeepsItsBraces(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"setup, empty block": {
			"group \"0\"\n    setup begin\n        begin end\n    end\n    test \"\"\n            assert\n        end\nend\n",
			"group \"0\"\n    setup begin\n        begin end\n    end\n\n    test \"\"\n        assert\n    end\nend\n",
		},
		"setup, nested empty blocks": {
			"group \"0\"\n    setup begin\n        begin\n            begin end\n        end\n    end test \"\"\n                assert\n            end\nend\n",
			"group \"0\"\n    setup begin\n        begin end\n    end\n\n    test \"\"\n        assert\n    end\nend\n",
		},
		"setup, block holding a defer": {
			"group \"0\"\n    setup begin\n        begin\n            defer f()\n 1\n        end\n    end test \"\"\n     assert\n end\nend\n",
			"group \"0\"\n    setup begin\n        begin\n            defer f()\n            1\n        end\n    end\n\n    test \"\"\n        assert\n    end\nend\n",
		},
		"lambda, empty block": {
			"fn f()\n    x = fn ->\n        begin end\n    end\nend\n",
			"fn f()\n    x = fn ->\n        begin end\n    end\nend\n",
		},
		"lambda with a parameter, empty block": {
			"fn f()\n    x = fn a ->\n        begin end\n    end\nend\n",
			"fn f()\n    x = fn a ->\n        begin end\n    end\nend\n",
		},
		"tail lambda, empty block": {
			"fn f()\n    g(1, fn ->\n        begin end\n    end)\nend\n",
			"fn f()\n    g(1, fn ->\n        begin end\n    end)\nend\n",
		},
		"lambda, block holding a defer": {
			"fn f()\n    x = fn ->\n        begin\n            defer g()\n 1\n        end\n    end\nend\n",
			"fn f()\n    x = fn ->\n        begin\n            defer g()\n            1\n        end\n    end\nend\n",
		},
		"clock": {
			"group \"g\"\n    clock begin\n        begin end\n    end test \"\"\n            assert\n        end\nend\n",
			"group \"g\"\n    clock begin\n        begin end\n    end\n\n    test \"\"\n        assert\n    end\nend\n",
		},
		"boot": {
			"group \"g\"\n    boot begin\n        begin end\n    end test \"\"\n            assert\n        end\nend\n",
			"group \"g\"\n    boot begin\n        begin end\n    end\n\n    test \"\"\n        assert\n    end\nend\n",
		},
		"test body": {
			"test \"\"\n    begin end\n    assert\nend\n",
			"test \"\"\n    begin end\n    assert\nend\n",
		},
		"fn body": {
			"fn f()\n    begin end\nend\n",
			"fn f()\n    begin end\nend\n",
		},
		"if branches": {
			"fn f()\n    y = if true then\n        begin end\n    else\n        begin end\n    end\nend\n",
			"fn f()\n    y = if true then\n        begin end\n    else\n        begin end\n    end\nend\n",
		},
		"case arm": {
			"fn f(x: Int)\n    case x\n        _ ->\n            begin end\n    end\nend\n",
			"fn f(x: Int)\n    case x\n        _ ->\n            begin end\n    end\nend\n",
		},
		"concurrent": {
			"fn f()\n    concurrent\n        begin end\n    end\nend\n",
			"fn f()\n    concurrent\n        begin end\n    end\nend\n",
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
