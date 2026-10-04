package format

import "testing"

// A `//#` module comment does not attach to what follows it, so one blank
// line separates a run of them from the next comment or declaration.
func TestFormat_BlankLineAfterModuleComment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "before a doc comment",
			in:   "//# Geometry.\n/// Area.\nfn area(): Int {\n    1\n}\n",
			want: "//# Geometry.\n\n/// Area.\nfn area(): Int {\n    1\n}\n",
		},
		{
			name: "a multi-line block before an import",
			in:   "//# Geometry.\n//#\n//# More.\nimport std/io\n\nfn main() {\n    io.print(1)\n}\n",
			want: "//# Geometry.\n//#\n//# More.\n\nimport std/io\n\nfn main() {\n    io.print(1)\n}\n",
		},
		{
			name: "before an import block",
			in:   "//# Geometry.\nimport std/io\nimport std/lists.List\n",
			want: "//# Geometry.\n\nimport {\n    std/io\n    std/lists.List\n}\n",
		},
		{
			name: "a blank line already there stays one",
			in:   "//# Geometry.\n\n\nfn area(): Int {\n    1\n}\n",
			want: "//# Geometry.\n\nfn area(): Int {\n    1\n}\n",
		},
		{
			name: "after the imports, before a plain comment",
			in:   "import std/io\n\n//# Section.\n// Plain.\nfn main() {\n    io.print(1)\n}\n",
			want: "import std/io\n\n//# Section.\n\n// Plain.\nfn main() {\n    io.print(1)\n}\n",
		},
		{
			name: "a plain comment still attaches",
			in:   "// Plain.\n\nfn area(): Int {\n    1\n}\n",
			want: "// Plain.\nfn area(): Int {\n    1\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("Format()\n got: %q\nwant: %q", got, c.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent\nfirst: %q\nagain: %q", got, again)
			}
		})
	}
}
