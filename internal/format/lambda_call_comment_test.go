package format

import "testing"

// A lambda whose body holds only comments keeps them. It used to be written
// `|x| {}` and the comments were dropped, whether the lambda was a call's
// last argument, an earlier or named one, a pipe stage's argument, a `then`
// or `tap` stage, or a bound value.
func TestFormat_CommentOnlyLambdaBodyKeepsItsComments(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "last argument",
			src:  "    r = f(a, |x| {\n        // c\n    })\n",
		},
		{
			name: "comment after the opening brace",
			src:  "    r = f(a, || { // c\n    })\n",
			want: "    r = f(a, || {\n        // c\n    })\n",
		},
		{
			name: "earlier argument",
			src:  "    r = f(\n        |x| {\n            // c\n        },\n        b,\n    )\n",
		},
		{
			name: "named argument",
			src:  "    r = f(\n        a,\n        by: |x| {\n            // c\n        },\n    )\n",
		},
		{
			name: "pipe stage argument",
			src:  "    r =\n        xs\n        |> Iter.map(|x| {\n            // c\n        })\n",
		},
		{
			name: "nested",
			src:  "    r = f(|x| {\n        g(x, |y| {\n            // c\n        })\n    })\n",
		},
		{
			name: "then stage",
			src:  "    r =\n        xs\n        |> then |x| {\n            // c\n        }\n",
		},
		{
			name: "tap stage",
			src:  "    r =\n        xs\n        |> tap |_| {\n            // c\n        }\n",
		},
		{
			name: "binding",
			src:  "    g = |x| {\n        // a\n        // b\n    }\n",
		},
		{
			name: "comment after the call too",
			src:  "    r = f(a, |x| {\n        // c\n    }) // d\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "fn main() {\n" + c.src + "}\n"
			want := src
			if c.want != "" {
				want = "fn main() {\n" + c.want + "}\n"
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
		"    r = f(a, |x| {\n        // c\n        x + 1\n    })\n",
		"    r = f(a, |x| {\n        x + 1\n        // c\n    })\n",
		"    r = f(a, |x| {\n        x + 1 // c\n    })\n",
		"    r = f(a, |x| {\n        x + 1\n    }) // c\n",
		"    r = f(a, |x| x + 1) // c\n",
		"    r = Mod.f<Int>(a, |x| {\n        // c\n        x\n    })\n",
		"    r = f(a, by: |x| {\n        // c\n        x + 1\n    })\n",
		"    r = f(\n        a,\n        |x| {\n            // c\n            x + 1\n        },\n        b,\n    )\n",
		"    r =\n        xs\n        |> Iter.map(|x| {\n            x + 1 // c\n        })\n        |> Iter.filter(|x| {\n            // d\n            x > 1\n        }) // e\n        |> Iter.to_list() // f\n",
		"    r = f(|x| {\n        // a\n        g(x, |y| {\n            // b\n            h(y, |z| {\n                // c\n                z\n            }) // d\n        }) // e\n    }) // f\n",
		"    r = f(a, |x| case x {\n        1 -> 2 // c\n        _ -> 3\n    })\n",
	}
	for _, body := range cases {
		src := "fn main() {\n" + body + "}\n"
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
