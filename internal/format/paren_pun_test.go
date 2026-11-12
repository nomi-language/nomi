package format

import "testing"

// A field whose value is its own name in parentheses, `t: (t)`, is punned
// on the first format. The formatter drops parentheses around a name, so
// it once wrote `t: t`, and only a second format punned that to `t`.

func TestFormat_ParenthesisedPunFieldIsPunnedAtOnce(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "fuzzer input",
			src:  "begin\nA{t:(t)}\nend\n0",
			want: "begin\n    A{t:}\nend\n\n0\n",
		},
		{
			name: "struct literal",
			src:  "fn f(t: Int)\n    A{t: (t), u: (t)}\nend\n",
			want: "fn f(t: Int)\n    A{t:, u: t}\nend\n",
		},
		{
			name: "nested parentheses",
			src:  "fn f(t: Int)\n    A{t: (((t)))}\nend\n",
			want: "fn f(t: Int)\n    A{t:}\nend\n",
		},
		{
			name: "anonymous struct",
			src:  "fn f(t: Int)\n    {t: (t)}\nend\n",
			want: "fn f(t: Int)\n    {t:}\nend\n",
		},
		{
			name: "struct update",
			src:  "fn f(b: A, t: Int)\n    {..b, t: (t)}\nend\n",
			want: "fn f(b: A, t: Int)\n    {..b, t:}\nend\n",
		},
		{
			name: "map entry keeps the key and value",
			src:  "fn f(t: Int)\n    {t => (t)}\nend\n",
			want: "fn f(t: Int)\n    {t => t}\nend\n",
		},
		{
			name: "struct pattern",
			src:  "fn f(b: A) -> Int\n    case b\n        A{t: (t), u:} -> t + u\n    end\nend\n",
			want: "fn f(b: A) -> Int\n    case b\n        A{t:, u:} -> t + u\n    end\nend\n",
		},
		{
			name: "struct pattern, nested parentheses",
			src:  "fn f(b: A) -> Int\n    case b\n        A{t: ((t))} -> t\n    end\nend\n",
			want: "fn f(b: A) -> Int\n    case b\n        A{t:} -> t\n    end\nend\n",
		},
		{
			name: "a comment in the parentheses keeps them",
			src:  "fn f(t: Int)\n    A{t: (t // c\n    ), u: 1}\nend\n",
			want: "fn f(t: Int)\n    A{t: (\n        t // c\n    ), u: 1}\nend\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			migrates(t, tc.src, tc.want)
		})
	}
}
