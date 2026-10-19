package format

import "testing"

// A field whose value is its own name in parentheses, `t: (t)`, is punned
// on the first format. The formatter drops parentheses around a name, so
// it once wrote `t: t`, and only a second format punned that to `t`.

func TestFormat_ParenthesisedPunFieldIsPunnedAtOnce(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "fuzzer input",
			src:  "{#A{t:(t)}} 0",
			want: "{ #A{t} }\n\n0\n",
		},
		{
			name: "struct literal",
			src:  "fn f(t: Int) {\n    #A{t: (t), u: (t)}\n}\n",
			want: "fn f(t: Int) {\n    #A{t, u: t}\n}\n",
		},
		{
			name: "nested parentheses",
			src:  "fn f(t: Int) {\n    #A{t: (((t)))}\n}\n",
			want: "fn f(t: Int) {\n    #A{t}\n}\n",
		},
		{
			name: "anonymous struct",
			src:  "fn f(t: Int) {\n    #{t: (t)}\n}\n",
			want: "fn f(t: Int) {\n    #{t}\n}\n",
		},
		{
			name: "struct update",
			src:  "fn f(b: A, t: Int) {\n    #{..b, t: (t)}\n}\n",
			want: "fn f(b: A, t: Int) {\n    #{..b, t}\n}\n",
		},
		{
			name: "map entry keeps the key and value",
			src:  "fn f(t: Int) {\n    %{t => (t)}\n}\n",
			want: "fn f(t: Int) {\n    %{t => t}\n}\n",
		},
		{
			name: "struct pattern",
			src:  "fn f(b: A) -> Int {\n    case b {\n        #A{t: (t), u} -> t + u\n    }\n}\n",
			want: "fn f(b: A) -> Int {\n    case b {\n        #A{t, u} -> t + u\n    }\n}\n",
		},
		{
			name: "struct pattern, nested parentheses",
			src:  "fn f(b: A) -> Int {\n    case b {\n        #A{t: ((t))} -> t\n    }\n}\n",
			want: "fn f(b: A) -> Int {\n    case b {\n        #A{t} -> t\n    }\n}\n",
		},
		{
			name: "a comment in the parentheses keeps them",
			src:  "fn f(t: Int) {\n    #A{t: (t // c\n    ), u: 1}\n}\n",
			want: "fn f(t: Int) {\n    #A{t: (\n        t // c\n    ), u: 1}\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			migrates(t, tc.src, tc.want)
		})
	}
}
