package format

import "testing"

// The formatter keeps a `#!` first line as it is and formats the rest. A
// blank line after it stays one blank line, and no blank line stays none.
func TestFormat_KeepsShebang(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no blank line after it",
			in:   "#!/usr/bin/env nomi\nimport std/io\nfn main() {\n  io.print(1)\n}\n",
			want: "#!/usr/bin/env nomi\nimport std/io\n\nfn main() {\n    io.print(1)\n}\n",
		},
		{
			name: "blank lines after it collapse to one",
			in:   "#!/usr/bin/env nomi\n\n\n// Says hi.\nfn main() {\n    Unit\n}\n",
			want: "#!/usr/bin/env nomi\n\n// Says hi.\nfn main() {\n    Unit\n}\n",
		},
		{
			name: "trailing whitespace and CRLF on it go",
			in:   "#!/usr/bin/env nomi  \r\nfn main() {\n    Unit\n}\n",
			want: "#!/usr/bin/env nomi\nfn main() {\n    Unit\n}\n",
		},
		{
			name: "a file that is only a shebang",
			in:   "#!/usr/bin/env nomi",
			want: "#!/usr/bin/env nomi\n",
		},
		{
			name: "only a shebang and a comment",
			in:   "#!/usr/bin/env nomi\n// nothing yet\n",
			want: "#!/usr/bin/env nomi\n// nothing yet\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(tc.in)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Format:\n got %q\nwant %q", got, tc.want)
			}
			again, err := Format(got)
			if err != nil || again != got {
				t.Fatalf("not idempotent: %q, %v", again, err)
			}
		})
	}
}

// A `#!` line anywhere but the start of the file is a syntax error, which
// the formatter reports instead of keeping.
func TestFormat_ShebangAfterFirstLineIsRejected(t *testing.T) {
	src := "fn main() {\n    Unit\n}\n#!/usr/bin/env nomi\n"
	if _, err := Format(src); err == nil {
		t.Fatal("Format accepted a #! line that is not the first line")
	}
}
