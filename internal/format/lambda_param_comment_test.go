package format

import "testing"

// A line comment after a lambda parameter's comma stays there, and the next
// parameter starts the next line. The parser takes a comment in the header
// only after a comma: written after the closing `|`, before a body on the
// next line, or on a line of its own before that body, the output did not
// parse. The source of each case is written by hand; its output must keep
// the program's meaning and come back unchanged from a second format.
func TestFormat_LambdaParameterCommentStaysAfterItsComma(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"after a default, body on the next line": {
			"fn main() {\n    body = Iter.reduce(xs, |acc = \"\", // c1\n frag|\n        case frag {\n            _ -> acc\n        }\n    )\n}\n",
			"fn main() {\n    body = Iter.reduce(xs, |acc = \"\", // c1\n        frag| {\n        case frag {\n            _ -> acc\n        }\n    })\n}\n",
		},
		"after a bare name, body on the next line": {
			"fn main() {\n    body = Iter.reduce(xs, |acc, // c1\n frag|\n        case frag {\n            _ -> acc\n        }\n    )\n}\n",
			"fn main() {\n    body = Iter.reduce(xs, |acc, // c1\n        frag| {\n        case frag {\n            _ -> acc\n        }\n    })\n}\n",
		},
		"after a destructured parameter": {
			"fn main() {\n    body = Iter.reduce(xs, |(p, q), // c1\n frag|\n        case frag {\n            _ -> p\n        }\n    )\n}\n",
			"fn main() {\n    body = Iter.reduce(xs, |(p, q), // c1\n        frag| {\n        case frag {\n            _ -> p\n        }\n    })\n}\n",
		},
		"after a bare name, one-line body": {
			"fn main() {\n    c = Iter.reduce(xs, |acc, // c1\n frag| acc)\n}\n",
			"fn main() {\n    c = Iter.reduce(xs, |acc, // c1\n        frag| {\n        acc\n    })\n}\n",
		},
		"after a bare name, before an annotated parameter": {
			"fn main() {\n    c = Iter.reduce(xs, |acc, // c1\n frag: Int| acc)\n}\n",
			"fn main() {\n    c = Iter.reduce(xs, |acc, // c1\n        frag: Int| {\n        acc\n    })\n}\n",
		},
		"after the first and the second of three": {
			"fn main() {\n    h = run(|a, // c1\n b, // c2\n c| a)\n}\n",
			"fn main() {\n    h = run(|a, // c1\n        b, // c2\n        c| {\n        a\n    })\n}\n",
		},
		"then stage, braced body": {
			"fn main() {\n    e = xs |> then |a, // c1\n b| { a + b }\n}\n",
			"fn main() {\n    e =\n        xs\n        |> then |a, // c1\n            b| {\n            a + b\n        }\n}\n",
		},
		"tap stage after a default": {
			"fn main() {\n    f = xs |> tap |a = 1, // c1\n b| { a + b }\n}\n",
			"fn main() {\n    f =\n        xs\n        |> tap |a = 1, // c1\n            b| {\n            a + b\n        }\n}\n",
		},
		"then stage, body on the next line": {
			"fn main() {\n    g = xs |> then |a = 1, // c1\n b|\n        case a {\n            _ -> b\n        }\n}\n",
			"fn main() {\n    g =\n        xs\n        |> then |a = 1, // c1\n            b| {\n            case a {\n                _ -> b\n            }\n        }\n}\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Format(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if err := SameMeaning(tc.src, got); err != nil {
				t.Fatalf("%v\nformatted:\n%s", err, got)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatalf("the output does not format: %v", err)
			}
			if again != got {
				t.Fatalf("a second format changed it:\n%s", again)
			}
		})
	}
}
