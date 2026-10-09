package format

import "testing"

// An own-line comment between top-level imports ends a section: combineImports
// combines each section into one block, and sortImports sorts only within a
// section, so no import moves across the comment. A blank line alone is not a
// boundary for combining. Inside an `import { … }` block a comment belongs to
// the entry below it and sorts with it. Each case must reach its fixed point
// in one format and keep its meaning.
func TestFormat_ImportCommentEndsSortSection(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "comment between, blank after it",
			src:  "import beta.parse\n// s1\n\nimport alpha.add\n",
			want: "import beta.parse\n// s1\nimport alpha.add\n",
		},
		{
			name: "comment between, no blank lines",
			src:  "import beta.parse\n// s1\nimport alpha.add\n",
			want: "import beta.parse\n// s1\nimport alpha.add\n",
		},
		{
			name: "comment between, blank lines around it",
			src:  "import beta.parse\n\n// s1\n\nimport alpha.add\n",
			want: "import beta.parse\n\n// s1\nimport alpha.add\n",
		},
		{
			name: "sections sort within themselves",
			src:  "import gamma.g\nimport beta.b\n// s1\nimport zeta.z\nimport alpha.a\n",
			want: "import {\n    beta.b\n    gamma.g\n}\n// s1\nimport {\n    alpha.a\n    zeta.z\n}\n",
		},
		{
			name: "comment before the first import",
			src:  "// header\nimport beta.parse\n\nimport alpha.add\n",
			want: "// header\nimport {\n    alpha.add\n    beta.parse\n}\n",
		},
		{
			name: "comment after the last import",
			src:  "import beta.parse\nimport alpha.add\n// tail\n\nfn main() {\n    Unit\n}\n",
			want: "import {\n    alpha.add\n    beta.parse\n}\n\n// tail\nfn main() {\n    Unit\n}\n",
		},
		{
			name: "comment inside a block moves with its entry",
			src:  "import {\n    beta.parse\n    // s1\n    alpha.add\n}\n",
			want: "import {\n    // s1\n    alpha.add\n    beta.parse\n}\n",
		},
		{
			name: "comment between, inside a function body",
			src:  "fn main() {\n    import beta.parse\n    // s1\n\n    import alpha.add\n\n    Unit\n}\n",
			want: "fn main() {\n    import beta.parse\n    // s1\n    import alpha.add\n\n    Unit\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.src)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatalf("Format (second pass): %v", err)
			}
			if again != got {
				t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", got, again)
			}
			if err := SameMeaning(c.src, got); err != nil {
				t.Errorf("meaning changed: %v", err)
			}
		})
	}
}
