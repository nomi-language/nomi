package format

import "testing"

// A run of blank lines immediately before a block's closing `}` is removed —
// symmetric with the already-stripped leading blank after the opener. Blank
// lines *between* statements are preserved.
func TestFormat_StripsTrailingBlankLinesInBlocks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "fn body, multiple trailing blanks",
			in:   "fn f()\n    io.print(1)\n\n\nend\n",
			want: "fn f()\n    io.print(1)\nend\n",
		},
		{
			name: "struct body trailing blank",
			in:   "struct Foo\n    a: Int\n\nend\n",
			want: "struct Foo\n    a: Int\nend\n",
		},
		{
			name: "multi-statement if block, trailing blank removed, stays broken",
			in:   "fn h(x: Int)\n    if x > 0 then\n        io.print(1)\n        io.print(2)\n\n    end\nend\n",
			want: "fn h(x: Int)\n    if x > 0 then\n        io.print(1)\n        io.print(2)\n    end\nend\n",
		},
		{
			// A short if/else whose only reason to stay broken was a trailing
			// blank collapses to one line once the blank is gone — and does so
			// in a single pass (idempotency, not a second-pass collapse).
			name: "short if/else with trailing blank collapses in one pass",
			in:   "fn h(x: Int) -> Int\n    if x > 0 then\n        1\n\n    else\n        2\n    end\nend\n",
			want: "fn h(x: Int) -> Int\n    if x > 0 then 1 else 2\nend\n",
		},
		{
			name: "empty block with only blank lines becomes {}",
			in:   "fn f() end\n",
			want: "fn f() end\n",
		},
		{
			name: "between-statement blank preserved",
			in:   "fn f()\n    io.print(1)\n\n    io.print(2)\nend\n",
			want: "fn f()\n    io.print(1)\n\n    io.print(2)\nend\n",
		},
		{
			name: "leading blank already stripped (regression guard)",
			in:   "fn f()\n\n    io.print(1)\nend\n",
			want: "fn f()\n    io.print(1)\nend\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Format()\n got: %q\nwant: %q", got, c.want)
			}
			got2, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if got2 != got {
				t.Errorf("not idempotent:\n 1st: %q\n 2nd: %q", got, got2)
			}
		})
	}
}
