package format

import "testing"

// A one-entry import block keeps its braces when a comment sits before its
// `}` or after it. Collapsing it to `import std/io` dropped both comments.
func TestFormat_OneEntryImportBlockKeepsItsComments(t *testing.T) {
	for name, src := range map[string]string{
		"before the brace": "import {\n    std/io\n    // more to come\n}\n\nfn main() {}\n",
		"after the brace":  "import {\n    std/io\n} // io only\n\nfn main() {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if got != src {
				t.Fatalf("got:\n%s\nwant:\n%s", got, src)
			}
			if err := SameMeaning(src, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}
