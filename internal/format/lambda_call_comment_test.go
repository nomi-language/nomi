package format

import "testing"

// A lambda whose body holds only comments keeps them. It used to be written
// `fn x -> {}` and the comments were dropped, whether the lambda was a call's
// last argument, an earlier or named one, a pipe stage's argument, a `then`
// or `tap` stage, or a bound value.
func TestFormat_CommentOnlyLambdaBodyKeepsItsComments(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "last argument",
			src:  "    r = f(a, fn x ->\n        // c\n    end)\n",
		},
		{
			name: "comment after the opening brace",
			src:  "    r = f(a, fn -> // c\n    end)\n",
			want: "    r = f(a, fn ->\n        // c\n    end)\n",
		},
		{
			name: "earlier argument",
			src:  "    r = f(\n        fn x ->\n            // c\n        end,\n        b,\n    )\n",
		},
		{
			name: "named argument",
			src:  "    r = f(\n        a,\n        by: fn x ->\n            // c\n        end,\n    )\n",
		},
		{
			name: "pipe stage argument",
			src:  "    r =\n        xs\n        |> Iter.map(fn x ->\n            // c\n        end)\n",
		},
		{
			name: "nested",
			src:  "    r = f(fn x ->\n        g(x, fn y ->\n            // c\n        end)\n    end)\n",
		},
		{
			name: "then stage",
			src:  "    r =\n        xs\n        |> then fn x ->\n            // c\n        end\n",
		},
		{
			name: "tap stage",
			src:  "    r =\n        xs\n        |> tap fn _ ->\n            // c\n        end\n",
		},
		{
			name: "binding",
			src:  "    g = fn x ->\n        // a\n        // b\n    end\n",
		},
		{
			name: "comment after the call too",
			src:  "    r = f(a, fn x ->\n        // c\n    end) // d\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "fn main()\n" +
				c.src +
				"end\n"
			want := src
			if c.want != "" {
				want = "fn main()\n" +
					c.want +
					"end\n"
			}
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
			if err := SameMeaning(src, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Comments around a lambda passed to a call are kept, and a second format
// changes nothing. The parser takes no comment inside an argument list
// outside a lambda's braces, so these are the places one can be: inside the
// braces, and after the call's `)`.
func TestFormat_CommentsAroundACallsLambdaArgument(t *testing.T) {
	cases := []string{
		"    r = f(a, fn x ->\n        // c\n        x + 1\n    end)\n",
		"    r = f(a, fn x ->\n        x + 1\n        // c\n    end)\n",
		"    r = f(a, fn x ->\n        x + 1 // c\n    end)\n",
		"    r = f(a, fn x ->\n        x + 1\n    end) // c\n",
		"    r = f(a, fn x -> x + 1) // c\n",
		"    r = Mod.f<Int>(a, fn x ->\n        // c\n        x\n    end)\n",
		"    r = f(a, by: fn x ->\n        // c\n        x + 1\n    end)\n",
		"    r = f(\n        a,\n        fn x ->\n            // c\n            x + 1\n        end,\n        b,\n    )\n",
		"    r =\n        xs\n        |> Iter.map(fn x ->\n            x + 1 // c\n        end)\n        |> Iter.filter(fn x ->\n            // d\n            x > 1\n        end) // e\n        |> Iter.to_list() // f\n",
		"    r = f(fn x ->\n        // a\n        g(x, fn y ->\n            // b\n            h(y, fn z ->\n                // c\n                z\n            end) // d\n        end) // e\n    end) // f\n",
		"    r = f(a, fn x -> case x\n        1 -> 2 // c\n        _ -> 3\n    end)\n",
	}
	for _, body := range cases {
		src := "fn main()\n" +
			body +
			"end\n"
		got, err := Format(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := SameMeaning(src, got); err != nil {
			t.Errorf("%v\nsource:\n%s\nformatted:\n%s", err, src, got)
			continue
		}
		if again, err := Format(got); err != nil || again != got {
			t.Errorf("a second format changes it:\n%s\nthen:\n%s", got, again)
		}
	}
}
