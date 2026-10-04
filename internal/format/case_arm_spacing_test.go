package format

import "testing"

// A case's arms sit on consecutive lines when they are laid out flat, and
// one blank line separates every two arms when they break. Either way the
// source's blank lines between arms are ignored and a comment stays above
// its arm.
func TestFormat_CaseArmSpacingFollowsTheLayout(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"flat arms written with blank lines": {
			src:  "fn f(x: Int): String {\n    case x {\n        1 -> \"one\"\n\n        2 -> \"two\"\n\n        _ -> \"many\"\n    }\n}\n",
			want: "fn f(x: Int): String {\n    case x {\n        1 -> \"one\"\n        2 -> \"two\"\n        _ -> \"many\"\n    }\n}\n",
		},
		"flat arms with several blank lines and a comment": {
			src:  "fn f(x: Int): String {\n    case x {\n        1 -> \"one\"\n\n\n        // two\n        2 -> \"two\"\n\n        _ -> \"many\"\n    }\n}\n",
			want: "fn f(x: Int): String {\n    case x {\n        1 -> \"one\"\n        // two\n        2 -> \"two\"\n        _ -> \"many\"\n    }\n}\n",
		},
		"broken arms written with no blank lines": {
			src:  "fn f(x: Int): String {\n    case x {\n        1 -> \"a string long enough that this arm cannot stay on one line with its pattern at any width\"\n        _ -> \"many\"\n    }\n}\n",
			want: "fn f(x: Int): String {\n    case x {\n        1 ->\n            \"a string long enough that this arm cannot stay on one line with its pattern at any width\"\n\n        _ ->\n            \"many\"\n    }\n}\n",
		},
		"broken arms with several blank lines and a comment": {
			src:  "fn f(x: Int): String {\n    case x {\n        1 -> \"a string long enough that this arm cannot stay on one line with its pattern at any width\"\n\n\n        // many\n        _ -> \"many\"\n    }\n}\n",
			want: "fn f(x: Int): String {\n    case x {\n        1 ->\n            \"a string long enough that this arm cannot stay on one line with its pattern at any width\"\n\n        // many\n        _ ->\n            \"many\"\n    }\n}\n",
		},
		"one arm": {
			src:  "fn f(x: Int): Int {\n    case x {\n        _ -> 0\n    }\n}\n",
			want: "fn f(x: Int): Int {\n    case x {\n        _ -> 0\n    }\n}\n",
		},
		"a one-statement block arm is a full block and breaks the others": {
			src:  "fn f(x: Maybe<Int>) {\n    case x {\n        Some(n) -> { assert n > 0 }\n        None -> assert False\n    }\n}\n",
			want: "fn f(x: Maybe<Int>) {\n    case x {\n        Some(n) -> {\n            assert n > 0\n        }\n\n        None ->\n            assert False\n    }\n}\n",
		},
		"subject-less case with flat arms": {
			src:  "fn f(x: Int): String {\n    case {\n        x > 0 -> \"positive\"\n\n        x < 0 -> \"negative\"\n        _ -> \"zero\"\n    }\n}\n",
			want: "fn f(x: Int): String {\n    case {\n        x > 0 -> \"positive\"\n        x < 0 -> \"negative\"\n        _ -> \"zero\"\n    }\n}\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Format(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent:\n%s\nthen:\n%s", got, again)
			}
		})
	}
}
