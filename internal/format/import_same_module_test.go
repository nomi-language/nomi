package format

import "testing"

// Separate import statements that name the same module merge into one entry
// (combineImports), unless the pair would bind a name or the module twice or
// both end in a same-line comment. The entries of one block print as
// written. The block printer once joined adjacent selective entries for one
// module onto one line on its own, keeping every name, so the fuzz input's
// `d.{A, A0}` and `d.{A0, A0}` became `d.{A, A0, A0, A0}` and SameMeaning
// rejected the changed syntax tree.
func TestFormat_SameModuleImportEntries(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"fuzz input", "import\nd.{A,A0}d.{A0,A0}\nend", "import\n    d.{A, A0}\n    d.{A0, A0}\nend\n"},
		{"block of two lists", "import\n    d.{A, B}\n    d.{C, D}\nend\n", "import\n    d.{A, B}\n    d.{C, D}\nend\n"},
		{"block of single names", "import\n    d.{A}\n    d.{B}\nend\n", "import\n    d.A\n    d.B\nend\n"},
		{"flat statements", "import d.{A}\nimport d.{B}\n", "import d.{A, B}\n"},
		{"flat bare and selective", "import d\nimport d.{A}\n", "import d.{self, A}\n"},
		{"flat shared name", "import d.{A, B}\nimport d.{B, C}\n", "import\n    d.{A, B}\n    d.{B, C}\nend\n"},
		{"flat fuzz input", "import d.{A, A0}\nimport d.{A0, A0}\n", "import\n    d.{A, A0}\n    d.{A0, A0}\nend\n"},
		{"flat module twice", "import d\nimport d\n", "import\n    d\n    d\nend\n"},
		{"flat self twice", "import d.{self, A}\nimport d.{self, B}\n", "import\n    d.{self, A}\n    d.{self, B}\nend\n"},
		{"flat third merges into first", "import d.A\nimport d.A\nimport d.B\n", "import\n    d.{A, B}\n    d.A\nend\n"},
		{"two same-line comments", "import d.{A, B} // x\nimport d.{C} // y\n", "import\n    d.{A, B} // x\n    d.C // y\nend\n"},
		{"one same-line comment", "import d.{A, B} // x\nimport d.{C}\n", "import d.{A, B, C} // x\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if err := SameMeaning(c.src, got); err != nil {
				t.Fatal(err)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("a second format changed it:\n%s\nthen:\n%s", got, again)
			}
		})
	}
}
